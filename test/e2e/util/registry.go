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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// registryTokenTimeout is the timeout of the token request to the registry.
const registryTokenTimeout = 30 * time.Second

// RegistryToken requests an anonymous pull token for the repository from the
// registry token service and returns the Authorization header value.
func RegistryToken(registry, repository string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), registryTokenTimeout)
	defer cancel()

	url := fmt.Sprintf("https://%s/token?scope=repository:%s:pull", registry, repository)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("request token from %s: unexpected status %d: %s", url, resp.StatusCode, body)
	}

	var token struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return "", fmt.Errorf("decode token from %s: %w", url, err)
	}

	if token.Token == "" {
		return "", fmt.Errorf("request token from %s: empty token", url)
	}

	return "Bearer " + token.Token, nil
}
