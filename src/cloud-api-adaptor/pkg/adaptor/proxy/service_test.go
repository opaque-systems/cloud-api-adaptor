// (C) Copyright Confidential Containers Contributors
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	b64 "encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsNodePublishVolumeTargetPath(t *testing.T) {
	volumePath := "/var/lib/kubelet/pods/abc/volumes/kubernetes.io~csi/pvc123/mount"
	directVolumesDir := t.TempDir()

	t.Run("Empty direct-volumes dir", func(t *testing.T) {
		assert.False(t, isNodePublishVolumeTargetPath(volumePath, directVolumesDir))
	})

	t.Run("Good path", func(t *testing.T) {
		err := prepareVolumeDir(directVolumesDir, volumePath)
		if err != nil {
			t.Errorf("Failed to add volume dir: %v", err)
		}

		assert.True(t, isNodePublishVolumeTargetPath(volumePath, directVolumesDir))
	})

	t.Run("Not CSI path", func(t *testing.T) {
		volumePath = "/var/lib/kubelet"

		err := prepareVolumeDir(directVolumesDir, volumePath)
		if err != nil {
			t.Errorf("Failed to add volume dir: %v", err)
		}

		assert.False(t, isNodePublishVolumeTargetPath(volumePath, directVolumesDir))
	})

	t.Run("Not much volumes/kubernetes.io~csi", func(t *testing.T) {
		volumePath = "/var/lib/kubelet/kubernetes.io~csi/12345/mount"

		err := prepareVolumeDir(directVolumesDir, volumePath)
		if err != nil {
			t.Errorf("Failed to add volume dir: %v", err)
		}

		assert.False(t, isNodePublishVolumeTargetPath(volumePath, directVolumesDir))
	})
}

func prepareVolumeDir(directVolumesDir, volumePath string) error {
	volumeDir := filepath.Join(directVolumesDir, b64.URLEncoding.EncodeToString([]byte(volumePath)))
	stat, err := os.Stat(volumeDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(volumeDir, 0700); err != nil {
			return err
		}
	}
	if stat != nil && !stat.IsDir() {
		return fmt.Errorf("%s should be a directory", volumeDir)
	}

	return nil
}

func TestSwap(t *testing.T) {
	var inout = []struct {
		in  string
		out string
	}{
		{"something", "something@sha256:abc123"},
		{"something:latest", "something@sha256:abc123"},
		{"something@sha256:abc123", "something@sha256:abc123"},
		{"something@sha256:abc456", "something@sha256:abc123"},

		{"docker.io/library/something", "docker.io/library/something@sha256:abc123"},
		{"docker.io/library/something:latest", "docker.io/library/something@sha256:abc123"},
		{"docker.io/library/something@sha256:abc123", "docker.io/library/something@sha256:abc123"},
		{"docker.io/library/something@sha256:abc456", "docker.io/library/something@sha256:abc123"},

		{"docker.io:8888/library/something", "docker.io:8888/library/something@sha256:abc123"},
		{"docker.io:8888/library/something:latest", "docker.io:8888/library/something@sha256:abc123"},
		{"docker.io:8888/library/something@sha256:abc123", "docker.io:8888/library/something@sha256:abc123"},
		{"docker.io:8888/library/something@sha256:abc456", "docker.io:8888/library/something@sha256:abc123"},
	}

	for _, tt := range inout {
		assert.Equal(t, tt.out, swapTagForDigest(tt.in, "sha256:abc123"))
	}
}
