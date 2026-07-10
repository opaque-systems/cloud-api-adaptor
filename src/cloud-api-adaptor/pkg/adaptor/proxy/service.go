// Copyright Confidential Containers Contributors
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/confidential-containers/cloud-api-adaptor/src/cloud-api-adaptor/pkg/util/agentproto"
	pb "github.com/kata-containers/kata-containers/src/runtime/virtcontainers/pkg/agent/protocols/grpc"
	"github.com/kata-containers/kata-containers/src/runtime/virtcontainers/types"
	"google.golang.org/protobuf/types/known/emptypb"
)

type proxyService struct {
	agentproto.Redirector
	pauseImage string
}

const (
	defaultPauseImage            = "registry.k8s.io/pause:3.7"
	kataDirectVolumesDir         = "/run/kata-containers/shared/direct-volumes"
	volumeTargetPathKey          = "io.confidentialcontainers.org.peerpodvolumes.target_path"
	csiPluginEscapeQualifiedName = "kubernetes.io~csi"
	imageGuestPull               = "image_guest_pull"
	cdiAnnotationKey             = "cdi.k8s.io/peer-pods"
	defaultCDIType               = "nvidia.com/gpu=all"
	defaultGPUsAnnotation        = "io.katacontainers.config.hypervisor.default_gpus"
	imageDigestsAnnotation       = "io.katacontainers.config.image-digests"
	imageDigestsModeAnnotation   = "io.katacontainers.config.image-digests-mode"
	imageDigestsModeByName       = "name"
)

func newProxyService(dialer func(context.Context) (net.Conn, error), pauseImage string) *proxyService {

	redirector := agentproto.NewRedirector(dialer)

	return &proxyService{
		Redirector: redirector,
		pauseImage: pauseImage,
	}
}

// AgentServiceService methods

func (s *proxyService) CreateContainer(ctx context.Context, req *pb.CreateContainerRequest) (*emptypb.Empty, error) {
	var pullImageInGuest bool
	logger.Printf("CreateContainer: containerID:%s", req.ContainerId)
	if len(req.OCI.Mounts) > 0 {
		logger.Print("    mounts:")
		for i, m := range req.OCI.Mounts {
			logger.Printf("        destination:%s source:%s type:%s", m.Destination, m.Source, m.Type)

			if isNodePublishVolumeTargetPath(m.Source, kataDirectVolumesDir) {
				if i > 0 {
					req.OCI.Annotations[volumeTargetPathKey] += ","
				}
				req.OCI.Annotations[volumeTargetPathKey] += m.Source
			}
		}
	}
	if len(req.OCI.Annotations) > 0 {
		logger.Print("    annotations:")
		for k, v := range req.OCI.Annotations {
			logger.Printf("        %s: %s", k, v)
		}
	}

	if len(req.Storages) > 0 {
		logger.Print("    storages:")
		for _, s := range req.Storages {
			logger.Printf("        mount_point:%s source:%s fstype:%s driver:%s", s.MountPoint, s.Source, s.Fstype, s.Driver)
			// remote-snapshotter in contanerd appends image_guest_pull drivers for image layer will be pulled in guest.
			// Image will be pull in guest via image-rs according to the driver info.
			if s.Driver == imageGuestPull {
				pullImageInGuest = true
			}
		}
	}
	if len(req.Devices) > 0 {
		logger.Print("    devices:")
		for _, d := range req.Devices {
			logger.Printf("        container_path:%s vm_path:%s type:%s", d.ContainerPath, d.VmPath, d.Type)
		}
	}

	if req.OCI.Annotations != nil && req.OCI.Annotations[defaultGPUsAnnotation] != "" {
		req.OCI.Annotations[cdiAnnotationKey] = defaultCDIType
		logger.Printf("adding CDI annotation %s: %s", cdiAnnotationKey, defaultCDIType)
	}

	if !pullImageInGuest {
		// There is some issue with nydus(error unpacking image) when the image layers are missing due to
		//  - discard_unpacked_layers set to true
		//  - other reasons we don't know yet
		// Run: ctr -n k8s.io image check
		// to see whether the image is complete(all layers are present)
		//
		// nydus adds one mount that carries the image information which is then picked up
		// by kata shim, then kata shim passes it to kata agent in the PodVM. Without nydus, we
		// have to add the mount point manually.
		vol, err := handleVirtualVolumeStorageObject(req)
		if err != nil {
			return nil, err
		}

		req.Storages = append(req.Storages, vol)
		s := req.Storages[len(req.Storages)-1]
		logger.Print("    storages added for guest_image_pull:")
		logger.Printf("        mount_point:%s source:%s fstype:%s driver:%s", s.MountPoint, s.Source, s.Fstype, s.Driver)
	}

	res, err := s.Redirector.CreateContainer(ctx, req)

	if err != nil {
		logger.Printf("CreateContainer fails: %v", err)
	}

	return res, err
}

// The flollowing fucntions are originally from kata_agent.go
//   - handleVirtualVolumeStorageObject
//   - handleImageGuestPullBlockVolume
//   - getContainerTypeforCRI
//
// Modified kata-containers/src/runtime/virtualcontainers/kata_agent.go::handleVirtualVolumeStorageObject
func handleVirtualVolumeStorageObject(req *pb.CreateContainerRequest) (*pb.Storage, error) {
	var vol *pb.Storage
	virtVolume := &types.KataVirtualVolume{
		VolumeType: types.KataVirtualVolumeImageGuestPullType,
		ImagePull: &types.ImagePullVolume{
			Metadata: map[string]string{},
		},
	}

	var err error
	vol = &pb.Storage{}
	vol, err = handleImageGuestPullBlockVolume(req.OCI.Annotations, virtVolume, vol)
	if err != nil {
		return nil, err
	}
	vol.MountPoint = filepath.Join("/run/kata-containers/", req.ContainerId, "rootfs")
	return vol, nil
}

// Modified kata-containers/src/runtime/virtualcontainers/kata_agent.go::handleImageGuestPullBlockVolume
func handleImageGuestPullBlockVolume(containerAnnotations map[string]string, virtualVolumeInfo *types.KataVirtualVolume, vol *pb.Storage) (*pb.Storage, error) {
	const ctrContainerType = "io.kubernetes.cri.container-type"
	const crioContainerType = "io.kubernetes.cri-o.ContainerType"
	const kubernetesCRIImageName = "io.kubernetes.cri.image-name"
	const kubernetesCRIOImageName = "io.kubernetes.cri-o.ImageName"
	const kubernetesCRIContainerName = "io.kubernetes.cri.container-name"
	const kubernetesCRIOContainerName = "io.kubernetes.cri-o.ContainerName"

	containerType, criContainerType := getContainerTypeforCRI(containerAnnotations)

	var imageRef, containerName string
	if containerType == "pod_sandbox" {
		imageRef = "pause"
	} else {

		switch criContainerType {
		case ctrContainerType:
			imageRef = containerAnnotations[kubernetesCRIImageName]
			containerName = containerAnnotations[kubernetesCRIContainerName]
		case crioContainerType:
			imageRef = containerAnnotations[kubernetesCRIOImageName]
			containerName = containerAnnotations[kubernetesCRIOContainerName]
		default:
			imageRef = containerAnnotations[kubernetesCRIImageName]
			containerName = containerAnnotations[kubernetesCRIContainerName]
		}

		if imageRef == "" {
			return nil, fmt.Errorf("Failed to get image name from annotations")
		}

		if digestsJSON, digestsOK := containerAnnotations[imageDigestsAnnotation]; digestsOK && digestsJSON != "" {
			var digests map[string]string
			if err := json.Unmarshal([]byte(digestsJSON), &digests); err != nil {
				return nil, fmt.Errorf("failed to parse %s annotation: %w", imageDigestsAnnotation, err)
			}

			var digest string
			var ok bool

			if containerAnnotations[imageDigestsModeAnnotation] == imageDigestsModeByName {
				digest, ok = digests[containerName]
			} else {
				digest, ok = digestForImageRef(imageRef, digests)
			}

			if ok && digest != "" {
				bits := strings.Split(digest, "@")
				if len(bits) != 2 {
					return nil, fmt.Errorf("invalid digest format for image %s: %s", imageRef, digest)
				}

				resolved := swapTagForDigest(imageRef, bits[1])
				logger.Printf("    replacing image %s with %s for container %s", imageRef, resolved, containerName)
				imageRef = resolved
			} else {
				logger.Printf("    no image replacement found for container %s image %s", containerName, imageRef)
			}
		} else {
			logger.Printf("    no image-digests annotation present, no replacement for container %s image %s", containerName, imageRef)
		}
	}

	switch criContainerType {
	case ctrContainerType:
		containerAnnotations[kubernetesCRIImageName] = imageRef
	case crioContainerType:
		containerAnnotations[kubernetesCRIOImageName] = imageRef
	default:
		containerAnnotations[kubernetesCRIImageName] = imageRef
	}

	virtualVolumeInfo.Source = imageRef

	//merge virtualVolumeInfo.ImagePull.Metadata and container_annotations
	for k, v := range containerAnnotations {
		virtualVolumeInfo.ImagePull.Metadata[k] = v
	}

	no, err := json.Marshal(virtualVolumeInfo.ImagePull)
	if err != nil {
		return nil, err
	}
	vol.Driver = types.KataVirtualVolumeImageGuestPullType
	vol.DriverOptions = append(vol.DriverOptions, types.KataVirtualVolumeImageGuestPullType+"="+string(no))
	vol.Source = virtualVolumeInfo.Source
	vol.Fstype = "overlay"
	return vol, nil
}

// digestForImageRef looks up imageRef in digests, falling back to suffix
// matching at "/" boundaries to handle registries prepended by Kubernetes that
// were not present in the original image reference (e.g. imageRef is
// "docker.io/library/nginx:1.27-alpine" but the map key is "nginx:1.27-alpine").
func digestForImageRef(imageRef string, digests map[string]string) (string, bool) {
	ref := imageRef
	for {
		if v, ok := digests[ref]; ok {
			return v, true
		}
		idx := strings.Index(ref, "/")
		if idx < 0 {
			return "", false
		}
		ref = ref[idx+1:]
	}
}

// swapTagForDigest returns imageRef with any existing tag or digest replaced by
// the supplied digest in canonical "name@digest" form. The registry portion (if
// any) is preserved so that a host:port prefix is not mistaken for a tag.
func swapTagForDigest(imageRef, digest string) string {
	prefix := ""
	namePart := imageRef

	if slashIdx := strings.LastIndex(imageRef, "/"); slashIdx >= 0 {
		prefix = imageRef[:slashIdx+1]
		namePart = imageRef[slashIdx+1:]
	}

	if atIdx := strings.Index(namePart, "@"); atIdx >= 0 {
		namePart = namePart[:atIdx]
	} else if colonIdx := strings.Index(namePart, ":"); colonIdx >= 0 {
		namePart = namePart[:colonIdx]
	}

	return prefix + namePart + "@" + digest
}

// Modified kata-containers/src/runtime/virtualcontainers/kata_agent.go::getContainerTypeforCRI
func getContainerTypeforCRI(containerAnnotations map[string]string) (string, string) {
	CRIContainerTypeKeyList := []string{
		"io.kubernetes.cri.container-type",
		"io.kubernetes.cri-o.ContainerType",
	}

	containerType := containerAnnotations["io.katacontainers.pkg.oci.container_type"]
	for _, key := range CRIContainerTypeKeyList {
		_, ok := containerAnnotations[key]
		if ok {
			return containerType, key
		}
	}
	return "", ""
}

func isNodePublishVolumeTargetPath(volumePath, directVolumesDir string) bool {
	if !strings.Contains(filepath.Clean(volumePath), "/volumes/"+csiPluginEscapeQualifiedName+"/") {
		return false
	}

	volumeDir := filepath.Join(directVolumesDir, b64.URLEncoding.EncodeToString([]byte(volumePath)))
	_, err := os.Stat(volumeDir)

	return err == nil
}

func (s *proxyService) SetPolicy(ctx context.Context, req *pb.SetPolicyRequest) (*emptypb.Empty, error) {

	logger.Printf("SetPolicy: policy:%s", req.Policy)

	res, err := s.Redirector.SetPolicy(ctx, req)

	if err != nil {
		logger.Printf("SetPolicy fails: %v", err)
	}

	return res, err
}

func (s *proxyService) StartContainer(ctx context.Context, req *pb.StartContainerRequest) (*emptypb.Empty, error) {

	logger.Printf("StartContainer: containerID:%s", req.ContainerId)

	res, err := s.Redirector.StartContainer(ctx, req)

	if err != nil {
		logger.Printf("StartContainer fails: %v", err)
	}

	return res, err
}

func (s *proxyService) RemoveContainer(ctx context.Context, req *pb.RemoveContainerRequest) (*emptypb.Empty, error) {

	logger.Printf("RemoveContainer: containerID:%s", req.ContainerId)

	res, err := s.Redirector.RemoveContainer(ctx, req)

	if err != nil {
		logger.Printf("RemoveContainer fails: %v", err)
	}

	return res, err
}

func (s *proxyService) CreateSandbox(ctx context.Context, req *pb.CreateSandboxRequest) (*emptypb.Empty, error) {

	logger.Printf("CreateSandbox: hostname:%s sandboxId:%s", req.Hostname, req.SandboxId)

	if len(req.Storages) > 0 {
		logger.Print("    storages:")
		for _, s := range req.Storages {
			logger.Printf("        mountpoint:%s source:%s fstype:%s driver:%s", s.MountPoint, s.Source, s.Fstype, s.Driver)
		}
	}

	res, err := s.Redirector.CreateSandbox(ctx, req)

	if err != nil {
		logger.Printf("CreateSandbox fails: %v", err)
	}

	return res, err
}

func (s *proxyService) DestroySandbox(ctx context.Context, req *pb.DestroySandboxRequest) (*emptypb.Empty, error) {

	logger.Printf("DestroySandbox")

	res, err := s.Redirector.DestroySandbox(ctx, req)

	if err != nil {
		logger.Printf("DestroySandbox fails: %v", err)
	}

	return res, err
}
