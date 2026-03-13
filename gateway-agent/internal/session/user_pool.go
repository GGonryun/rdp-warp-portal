package session

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"sync"

	"github.com/p0rtal-4/gateway-agent/internal/config"
)

// UserPool manages a fixed set of local Windows user accounts that are
// assigned to RDP bastion sessions on demand.
type UserPool struct {
	mu    sync.Mutex
	users []string            // all available usernames
	inUse map[string]string   // username → session ID
}

// NewUserPool reads the user-pool.json file referenced by cfg.UserPoolFile,
// parses it into a UserPoolConfig, and initialises the pool.
func NewUserPool(cfg *config.Config) (*UserPool, error) {
	data, err := os.ReadFile(cfg.UserPoolFile)
	if err != nil {
		return nil, fmt.Errorf("read user pool file: %w", err)
	}

	// Strip UTF-8 BOM that PowerShell's Out-File adds by default.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	var poolCfg UserPoolConfig
	if err := json.Unmarshal(data, &poolCfg); err != nil {
		return nil, fmt.Errorf("parse user pool file: %w", err)
	}

	users := poolCfg.Users
	// If no explicit user list was provided but a prefix and count were,
	// generate the usernames from the pattern.
	if len(users) == 0 && poolCfg.Prefix != "" && poolCfg.Count > 0 {
		users = make([]string, poolCfg.Count)
		for i := 0; i < poolCfg.Count; i++ {
			users[i] = fmt.Sprintf("%s%d", poolCfg.Prefix, i+1)
		}
	}

	if len(users) == 0 {
		return nil, fmt.Errorf("user pool is empty: no users defined")
	}

	return &UserPool{
		users: users,
		inUse: make(map[string]string),
	}, nil
}

// Acquire finds the first unused user in the pool, generates a new secure
// password, resets the Windows account password, marks the user as in-use for
// the given session, and returns the credentials.
func (p *UserPool) Acquire(sessionID string) (username string, password string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Find the first user that is not currently in use.
	var found string
	for _, u := range p.users {
		if _, occupied := p.inUse[u]; !occupied {
			found = u
			break
		}
	}
	if found == "" {
		return "", "", fmt.Errorf("user pool exhausted: all %d users are in use", len(p.users))
	}

	pwd := generateSecurePassword(24)

	if err := setWindowsUserPassword(found, pwd); err != nil {
		return "", "", fmt.Errorf("reset password for %s: %w", found, err)
	}

	p.inUse[found] = sessionID

	return found, pwd, nil
}

// Release returns a user account to the pool so it can be reused.
func (p *UserPool) Release(username string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.inUse, username)
}

// Available returns the number of users that are not currently assigned to a
// session.
func (p *UserPool) Available() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.users) - len(p.inUse)
}

// InUse returns the number of users currently assigned to sessions.
func (p *UserPool) InUse() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.inUse)
}

// generateSecurePassword produces a cryptographically random alphanumeric
// string of the requested length using crypto/rand.
func generateSecurePassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := 0; i < length; i++ {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			// Falling back is not ideal, but crypto/rand failures are
			// exceedingly rare and indicate a broken OS RNG.
			panic(fmt.Sprintf("crypto/rand failed: %v", err))
		}
		result[i] = charset[idx.Int64()]
	}
	return string(result)
}

// setWindowsUserPassword resets the password of a local Windows user account
// via the built-in "net user" command.
func setWindowsUserPassword(username, password string) error {
	cmd := exec.Command("net", "user", username, password)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("net user %s: %s: %w", username, string(output), err)
	}
	return nil
}
