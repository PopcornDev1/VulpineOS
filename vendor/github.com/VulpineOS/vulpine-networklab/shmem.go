package networklab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	shmemFileName = "network-identities.dat"
	shmemDirName  = ".vulpineos"
)

// shmemPath returns the shared memory file path under $HOME.
func shmemPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, shmemDirName, shmemFileName)
}

// ensureShmemDir creates the .vulpineos directory if needed.
func ensureShmemDir() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("networklab: cannot determine home dir: %w", err)
	}
	dir := filepath.Join(home, shmemDirName)
	return os.MkdirAll(dir, 0700)
}

// writeShmem writes raw bytes to the shared memory file.
func writeShmem(data []byte) error {
	return os.WriteFile(shmemPath(), data, 0600)
}

// BuildIdentityParamsJSON builds the JSON string with hex-formatted values
// that the C++ NetworkIdentityManager::ParseHexArray can consume.
// Format: {"cipher_suites":[0x1301,0x1302,...],"supported_groups":[...],...}
func BuildIdentityParamsJSON(id *Identity) string {
	p := id.inner
	var b strings.Builder
	b.WriteByte('{')

	writeArray := func(name string, vals []uint16) {
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.WriteString(fmt.Sprintf(`"%s":[`, name))
		for i, v := range vals {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "0x%04X", v)
		}
		b.WriteByte(']')
	}

	writeArray("cipher_suites", p.CipherSuites)
	writeArray("supported_groups", p.SupportedGroups)
	writeArray("sig_algs", p.SigAlgs)
	writeArray("key_share_curves", p.KeyShareCurves)

	// Extension type IDs for identity-driven extension order/filter
	if len(p.Extensions) > 0 {
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.WriteString(`"extensions":[`)
		for i, ext := range p.Extensions {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "0x%04X", ext.Type)
		}
		b.WriteByte(']')
	}

	b.WriteByte('}')
	return b.String()
}

// BuildMultiIdentityJSON builds the full per-context map in hex format:
// {"ctx-1":{"cipher_suites":[0x1301,...],...},"ctx-2":{...}}
func BuildMultiIdentityJSON(identities map[string]*Identity) string {
	var b strings.Builder
	b.WriteByte('{')
	first := true
	for ctxID, id := range identities {
		if !first {
			b.WriteByte(',')
		}
		first = false
		key, _ := json.Marshal(ctxID)
		b.Write(key)
		b.WriteByte(':')
		b.WriteString(BuildIdentityParamsJSON(id))
	}
	b.WriteByte('}')
	return b.String()
}
