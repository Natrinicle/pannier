package config

import "strings"

const redacted = "***"

// Redact returns a copy safe to snapshot as pannier.self. Secret file
// *paths* stay (they are not the secret). Inline keys and anything that
// looks like a password field are scrubbed.
func Redact(f File) File {
	out := f
	out.Remotes = redactRemotes(f.Remotes)
	out.Satchels = make([]Satchel, len(f.Satchels))
	copy(out.Satchels, f.Satchels)
	for i := range out.Satchels {
		out.Satchels[i].Remotes = redactRemotes(out.Satchels[i].Remotes)
	}
	return out
}

func redactRemotes(in []Remote) []Remote {
	out := make([]Remote, len(in))
	copy(out, in)
	for i := range out {
		out[i].IngestCreds = redactCreds(out[i].IngestCreds)
		out[i].MaintenanceCreds = redactCreds(out[i].MaintenanceCreds)
	}
	return out
}

func redactCreds(c *Creds) *Creds {
	if c == nil {
		return nil
	}
	cp := *c
	if cp.AccessKey != "" {
		cp.AccessKey = redacted
	}
	if cp.SecretKey != "" {
		cp.SecretKey = redacted
	}
	return &cp
}

func LooksSecret(key, value string) bool {
	k := strings.ToLower(key)
	if value == "" {
		return false
	}
	for _, n := range []string{"password", "secret", "token", "passphrase", "accesskey"} {
		if strings.Contains(k, n) {
			return true
		}
	}
	return false
}
