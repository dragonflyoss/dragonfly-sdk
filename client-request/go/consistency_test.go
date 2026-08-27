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

// Cross-language consistency vectors shared with the Rust SDK
// (client-request/rust/tests/consistency.rs). Both test suites assert the same
// fixed outputs, so a change on either side that breaks hashring or task id
// compatibility fails the build.

package request

import (
	"encoding/binary"
	"testing"

	"d7y.io/dragonfly/v2/pkg/idgen"
	"github.com/dchest/siphash"
	"github.com/stretchr/testify/assert"

	"d7y.io/dragonfly-sdk/client-request/go/internal/hashring"
)

func vnodeHash(id uint64, name string) uint64 {
	buf := make([]byte, 0, 8+len(name)+1)
	buf = binary.LittleEndian.AppendUint64(buf, id)
	buf = append(buf, name...)
	buf = append(buf, 0xff)
	return siphash.Hash(0, 0, buf)
}

func keyHash(key string) uint64 {
	buf := make([]byte, 0, len(key)+1)
	buf = append(buf, key...)
	buf = append(buf, 0xff)
	return siphash.Hash(0, 0, buf)
}

func TestSiphash(t *testing.T) {
	assert := assert.New(t)

	assert.Equal(uint64(0x1e5a582b8d945969), vnodeHash(0, "seed-peer-1"))
	assert.Equal(uint64(0xb5db98265419376c), vnodeHash(1, "seed-peer-1"))
	assert.Equal(uint64(0xf36f748c486b09ef), vnodeHash(0, "seed-peer-2"))
	assert.Equal(uint64(0xf0eda07426c5cac1), vnodeHash(511, "seed-peer-3"))

	assert.Equal(uint64(0xaa86f08bc4878a68), keyHash("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"))
	assert.Equal(uint64(0xa78dd821b4023714), keyHash("test-task"))
	assert.Equal(uint64(0x20dd4eb33d9590f2), keyHash(""))
}

func TestHashring(t *testing.T) {
	assert := assert.New(t)

	ring := hashring.New(3)
	for _, name := range []string{"seed-peer-1", "seed-peer-2", "seed-peer-3"} {
		ring.Add(name)
	}

	sel := func(key string, replicas int) []string {
		var out []string
		for _, vnode := range ring.GetWithReplicas(key, replicas) {
			out = append(out, vnode.String())
		}
		return out
	}

	assert.Equal([]string{"seed-peer-3|2", "seed-peer-1|2", "seed-peer-3|0"}, sel("task-a", 2))
	assert.Equal([]string{"seed-peer-2|0", "seed-peer-2|1", "seed-peer-1|0"}, sel("task-b", 2))
	assert.Equal([]string{"seed-peer-2|1", "seed-peer-1|0", "seed-peer-3|2"}, sel("task-c", 2))
	assert.Equal([]string{
		"seed-peer-3|2",
		"seed-peer-1|2",
		"seed-peer-3|0",
		"seed-peer-1|1",
		"seed-peer-2|2",
		"seed-peer-3|1",
		"seed-peer-2|0",
		"seed-peer-2|1",
		"seed-peer-1|0",
	}, sel("task-a", 100))
}

func TestTaskID(t *testing.T) {
	assert := assert.New(t)

	pieceLength := uint64(4194304)
	id, err := idgen.TaskIDV2("https://example.com/file.txt?Expires=e1&Signature=s1&foo=bar", &pieceLength, "tag1", "app1", []string{"Expires", "Signature"}, "", true)
	assert.NoError(err)
	assert.Equal("2a0c4c713d7f2f65f36b78b79c4b78a6bf5d5f67b76730ed13485d3271482f1c", id)

	id, err = idgen.TaskIDV2("https://example.com/file.txt", nil, "", "", nil, "", true)
	assert.NoError(err)
	assert.Equal("7fcf06e5f0b1e443065c1a563eed788eb2e168a05c6ad9c4b319f7a976322be0", id)

	id, err = idgen.TaskIDV2("https://example.com/file.txt?z=9&b=2&a=1", nil, "foo", "bar", []string{"z"}, "", true)
	assert.NoError(err)
	assert.Equal("8b3f6e9b9b8fe20903bced565cfd1d0aaef354a4c17573f0c2c1979210443f9d", id)

	id, err = idgen.TaskIDV2("https://example.com/file.txt?b=2&a=1&b=1", nil, "", "", []string{"c"}, "", true)
	assert.NoError(err)
	assert.Equal("7c8801d0596be5e8f9449d5c4af23866c72fe5205119c0e5912981f3b16a37aa", id)

	pieceLength = uint64(1024)
	id, err = idgen.TaskIDV2("https://example.com/file.txt?k=a b&m=x*y&n=c~d", &pieceLength, "", "", []string{"none"}, "", true)
	assert.NoError(err)
	assert.Equal("6196a6846023f6d3c1e4d30f6c86f3d4186e4c664a33e5692b0e04e49b26a9af", id)

	id, err = idgen.TaskIDV2("https://example.com/file.txt?a=1&b=2", nil, "foo", "", []string{"a", "b"}, "", true)
	assert.NoError(err)
	assert.Equal("c8f4b41117329d54af920010394f6f607bac707e933ab2f18d372e3dd4c7fcb3", id)

	id, err = idgen.TaskIDV2("https://example.com/file.txt?b=2&a=1", nil, "", "", nil, "", true)
	assert.NoError(err)
	assert.Equal("980ee327518ccc5a7c30703e1a2232e8ba9047b39431f940636c85b6146f8b9a", id)

	id, err = idgen.TaskIDV2("https://example.com/file.txt?a=1;x&b=2", nil, "", "", []string{"none"}, "", true)
	assert.NoError(err)
	assert.Equal("370933e9d7ab42b3ed90287213cd5f8def6091c8606da5b2dc79436061b06c34", id)

	id, err = idgen.TaskIDV2("http://www.xx.yy/path?u=f&x=y&m=z&x=s#size", nil, "", "", []string{"x", "m"}, "", true)
	assert.NoError(err)
	assert.Equal("3570c3c808f06fd250a7a60634b9275d72c56edc248576508bb06264c2c65825", id)

	id, err = idgen.TaskIDV2("", nil, "", "", nil, "This is a test file", true)
	assert.NoError(err)
	assert.Equal("e2d0fe1585a63ec6009c8016ff8dda8b17719a637405a4e23c0ff81339148249", id)

	id, err = idgen.TaskIDV2("http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e", nil, "", "", nil, "", true)
	assert.NoError(err)
	assert.Equal("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e", id)

	id, err = idgen.TaskIDV2("http://registry.example.com/v2/library/ubuntu/manifests/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e", nil, "", "", nil, "", true)
	assert.NoError(err)
	assert.Equal("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e", id)

	id, err = idgen.TaskIDV2("http://localhost:5000/v2/myrepo/manifests/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e?ns=docker.io", nil, "", "", nil, "", true)
	assert.NoError(err)
	assert.Equal("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e", id)

	// A manifest url referenced by a tag falls back to the url based task id.
	id, err = idgen.TaskIDV2("http://registry.example.com/v2/library/ubuntu/manifests/latest", nil, "", "", nil, "", true)
	assert.NoError(err)
	assert.Equal("0b6500b31c7eea2929393e154299bc81ebbb613c24fa7f5f33e893d585f4d629", id)
}
