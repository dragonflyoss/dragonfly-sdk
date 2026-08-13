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

// Package oci resolves OCI image manifests from a registry for preheating,
// ported from the dragonfly manager's preheat implementation
// (internal/job/image.go).
package oci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/containerd/platforms"
	"github.com/distribution/reference"
	"github.com/docker/distribution"
	"github.com/docker/distribution/manifest/manifestlist"
	"github.com/docker/distribution/manifest/ocischema"
	"github.com/docker/distribution/manifest/schema1"
	"github.com/docker/distribution/manifest/schema2"
	registryclient "github.com/docker/distribution/registry/client"
	"github.com/docker/distribution/registry/client/auth"
	"github.com/docker/distribution/registry/client/transport"
	typesregistry "github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/registry"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// defaultRegistryTimeout is the default timeout for registry requests.
const defaultRegistryTimeout = time.Minute

// dockerRegistryHost is the actual registry host for docker.io references.
const dockerRegistryHost = "registry-1.docker.io"

// ResolveOptions is the options for resolving an image.
type ResolveOptions struct {
	// Image is the OCI image reference (e.g., "docker.io/library/nginx:latest").
	Image string

	// Username is the username for registry authentication.
	Username string

	// Password is the password for registry authentication.
	Password string

	// Platform is the target platform in the format "os/arch", default is the
	// current platform.
	Platform string
}

// ResolveResult is the result of resolving an image.
type ResolveResult struct {
	// BlobURLs is the blob urls (config and layers) of the image.
	BlobURLs []string

	// Token is the authorization header value for downloading the blobs.
	Token string
}

// preheatImage is image information for preheat.
type preheatImage struct {
	protocol string
	domain   string
	name     string
	tag      string
}

func (p *preheatImage) manifestURL() string {
	return fmt.Sprintf("%s://%s/v2/%s/manifests/%s", p.protocol, p.domain, p.name, p.tag)
}

func (p *preheatImage) blobURL(digest string) string {
	return fmt.Sprintf("%s://%s/v2/%s/blobs/%s", p.protocol, p.domain, p.name, digest)
}

// parseImage parses an image reference into its manifest components. The
// reference is normalized with docker conventions, so "nginx:latest" resolves
// to "docker.io/library/nginx:latest".
func parseImage(image string) (*preheatImage, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}
	named = reference.TagNameOnly(named)

	var tag string
	switch ref := named.(type) {
	case reference.Digested:
		tag = ref.Digest().String()
	case reference.Tagged:
		tag = ref.Tag()
	default:
		return nil, fmt.Errorf("invalid image reference: %s", image)
	}

	domain := reference.Domain(named)
	if domain == "docker.io" {
		domain = dockerRegistryHost
	}

	return &preheatImage{
		protocol: "https",
		domain:   domain,
		name:     reference.Path(named),
		tag:      tag,
	}, nil
}

// Resolve resolves the image manifest from the registry and returns the blob
// urls (config and layers) along with the authorization token for downloading
// them.
func Resolve(ctx context.Context, opts *ResolveOptions) (*ResolveResult, error) {
	image, err := parseImage(opts.Image)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{
		Timeout:   defaultRegistryTimeout,
		Transport: http.DefaultTransport.(*http.Transport).Clone(),
	}

	client, err := newImageAuthClient(image, httpClient, &typesregistry.AuthConfig{Username: opts.Username, Password: opts.Password})
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate with registry: %w", err)
	}

	platform := platforms.DefaultSpec()
	if opts.Platform != "" {
		platform, err = platforms.Parse(opts.Platform)
		if err != nil {
			return nil, fmt.Errorf("invalid platform format %q, expected \"os/arch\" (e.g., \"linux/amd64\"): %w", opts.Platform, err)
		}
	}

	manifests, err := resolveManifests(ctx, client, image, platform)
	if err != nil {
		return nil, fmt.Errorf("failed to pull image manifest: %w", err)
	}

	if len(manifests) == 0 {
		return nil, fmt.Errorf("no matching manifest for platform %s", platforms.Format(platform))
	}

	var blobURLs []string
	for _, m := range manifests {
		for _, v := range m.References() {
			blobURLs = append(blobURLs, image.blobURL(v.Digest.String()))
		}
	}

	return &ResolveResult{
		BlobURLs: blobURLs,
		Token:    client.GetAuthToken(),
	}, nil
}

// resolveManifests fetches and resolves container image manifests from a
// registry for a specified platform. Supports single manifests and manifest
// lists.
func resolveManifests(ctx context.Context, client *imageAuthClient, image *preheatImage, platform specs.Platform) ([]distribution.Manifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, image.manifestURL(), nil)
	if err != nil {
		return nil, err
	}

	// Set accept header with media types.
	for _, mediaType := range distribution.ManifestMediaTypes() {
		req.Header.Add("Accept", mediaType)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Handle response.
	if resp.StatusCode == http.StatusNotModified {
		return nil, distribution.ErrManifestNotModified
	} else if !registryclient.SuccessStatus(resp.StatusCode) {
		return nil, registryclient.HandleErrorResponse(resp)
	}

	ctHeader := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Unmarshal manifest.
	manifest, _, err := distribution.UnmarshalManifest(ctHeader, body)
	if err != nil {
		return nil, err
	}

	switch v := manifest.(type) {
	case *schema1.SignedManifest, *schema2.DeserializedManifest, *ocischema.DeserializedManifest:
		return []distribution.Manifest{v}, nil
	case *manifestlist.DeserializedManifestList:
		var result []distribution.Manifest
		for _, desc := range filterManifests(v.Manifests, platform) {
			image.tag = desc.Digest.String()
			manifests, err := resolveManifests(ctx, client, image, platform)
			if err != nil {
				return nil, err
			}

			result = append(result, manifests...)
		}

		return result, nil
	}

	return nil, errors.New("unknown manifest type")
}

// filterManifests filters a list of manifest descriptors to return only those
// matching the specified platform.
func filterManifests(manifests []manifestlist.ManifestDescriptor, platform specs.Platform) []manifestlist.ManifestDescriptor {
	var matches []manifestlist.ManifestDescriptor
	for _, desc := range manifests {
		if desc.Platform.Architecture == platform.Architecture && desc.Platform.OS == platform.OS {
			matches = append(matches, desc)
		}
	}

	return matches
}

// imageAuthClient is a client for image authentication.
type imageAuthClient struct {
	// httpClient is the http client.
	httpClient *http.Client

	// authConfig is the auth config.
	authConfig *typesregistry.AuthConfig

	// interceptorTokenHandler is the token interceptor.
	interceptorTokenHandler *interceptorTokenHandler
}

// newImageAuthClient creates a new imageAuthClient whose transport negotiates
// v2 authentication with the registry and intercepts the issued bearer token.
func newImageAuthClient(image *preheatImage, httpClient *http.Client, authConfig *typesregistry.AuthConfig) (*imageAuthClient, error) {
	d := &imageAuthClient{
		httpClient:              httpClient,
		authConfig:              authConfig,
		interceptorTokenHandler: newInterceptorTokenHandler(),
	}

	// New a challenge manager for the supported authentication types.
	challengeManager, err := registry.PingV2Registry(&url.URL{Scheme: image.protocol, Host: image.domain}, d.httpClient.Transport)
	if err != nil {
		return nil, err
	}

	// New a credential store which always returns the same credential values.
	creds := registry.NewStaticCredentialStore(d.authConfig)

	// Transport with authentication.
	d.httpClient.Transport = transport.NewTransport(
		d.httpClient.Transport,
		auth.NewAuthorizer(
			challengeManager,
			auth.NewTokenHandlerWithOptions(auth.TokenHandlerOptions{
				Transport:   d.httpClient.Transport,
				Credentials: creds,
				Scopes: []auth.Scope{auth.RepositoryScope{
					Repository: image.name,
					Actions:    []string{"pull"},
				}},
				ClientID: registry.AuthClientID,
			}),
			d.interceptorTokenHandler,
			auth.NewBasicHandler(creds),
		),
	)

	return d, nil
}

// Do sends an HTTP request and returns an HTTP response.
func (d *imageAuthClient) Do(req *http.Request) (*http.Response, error) {
	return d.httpClient.Do(req)
}

// GetAuthToken returns the bearer token.
func (d *imageAuthClient) GetAuthToken() string {
	return d.interceptorTokenHandler.GetAuthToken()
}

// interceptorTokenHandler is a token interceptor that intercepts the bearer
// token from the auth handler.
type interceptorTokenHandler struct {
	auth.AuthenticationHandler
	token string
}

// newInterceptorTokenHandler returns a new interceptorTokenHandler.
func newInterceptorTokenHandler() *interceptorTokenHandler {
	return &interceptorTokenHandler{}
}

// Scheme returns the authentication scheme.
func (h *interceptorTokenHandler) Scheme() string {
	return "bearer"
}

// AuthorizeRequest saves the Authorization header from the request.
func (h *interceptorTokenHandler) AuthorizeRequest(req *http.Request, params map[string]string) error {
	h.token = req.Header.Get("Authorization")
	return nil
}

// GetAuthToken returns the bearer token.
func (h *interceptorTokenHandler) GetAuthToken() string {
	return h.token
}
