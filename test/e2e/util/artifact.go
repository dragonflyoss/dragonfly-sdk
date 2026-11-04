/*
 *     Copyright 2026 The Dragonfly Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package util

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultFileMode = os.FileMode(0664)
	defaultDirMode  = os.FileMode(0775)
)

// UploadArtifactStdout saves the stdout of the pod to the artifact directory.
func UploadArtifactStdout(namespace, podName, logDirName string) error {
	out, err := KubeCtlCommand("-n", namespace, "logs", podName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("get logs of %s/%s: %w, output: %s", namespace, podName, err, out)
	}

	logDir := filepath.Join(ArtifactDir, logDirName)
	if err := os.MkdirAll(logDir, defaultDirMode); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(logDir, fmt.Sprintf("%s-stdout.log", podName)), out, defaultFileMode)
}
