package credentials

// CredentialsFile represents the top-level credentials.json structure.
type CredentialsFile struct {
	Targets []Target `json:"targets"`
}

// Target represents a target host with its connection credentials.
type Target struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	Domain   string   `json:"domain"`
	Tags     []string `json:"tags"`
}

// TargetSafe is a Target without sensitive fields, safe for API responses.
type TargetSafe struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Host string   `json:"host"`
	Port int      `json:"port"`
	Tags []string `json:"tags"`
}

// Safe returns a TargetSafe copy with sensitive fields stripped.
func (t *Target) Safe() TargetSafe {
	return TargetSafe{
		ID:   t.ID,
		Name: t.Name,
		Host: t.Host,
		Port: t.Port,
		Tags: t.Tags,
	}
}
