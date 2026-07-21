package webrtccred

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/callvoice/callvoice/services/edge/internal/agent"
	"github.com/callvoice/callvoice/services/edge/internal/fs"
)

const (
	defaultTTL     = 2 * time.Hour
	credKeyPrefix  = "agent:"
	credKeySuffix  = ":webrtc"
	passwordBytes  = 24
)

// ErrNoCreds means the agent has no active WebRTC credentials.
var ErrNoCreds = errors.New("webrtc credentials not found")

// Config is returned to the browser softphone.
type Config struct {
	WSSURL     string      `json:"wssUrl"`
	SIPURI     string      `json:"sipUri"`
	Password   string      `json:"password"`
	ICEServers []ICEServer `json:"iceServers"`
}

// ICEServer is a STUN/TURN entry for RTCPeerConnection.
type ICEServer struct {
	URLs []string `json:"urls"`
}

type storedCred struct {
	SIPUser   string    `json:"sipUser"`
	Password  string    `json:"password"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Provisioner issues ephemeral FreeSWITCH directory users for WebRTC agents.
type Provisioner struct {
	ESL          fs.APIClient
	DirectoryDir string
	WSSURL       string
	SIPDomain    string
	ICEServers   []ICEServer
	TTL          time.Duration
	RDB          *redis.Client
	Pres         *agent.Presence
	Now          func() time.Time
}

// Issue provisions a directory user agent-{uuid} with a random password (TTL 2h).
func (p *Provisioner) Issue(ctx context.Context, userID uuid.UUID) (*Config, error) {
	if p.TTL <= 0 {
		p.TTL = defaultTTL
	}
	now := p.now()
	sipUser := "agent-" + userID.String()
	password, err := randomPassword()
	if err != nil {
		return nil, err
	}
	expires := now.Add(p.TTL)

	if err := p.writeDirectoryUser(sipUser, password); err != nil {
		return nil, err
	}
	if _, err := p.ESL.API("reloadxml"); err != nil {
		return nil, fmt.Errorf("reloadxml: %w", err)
	}

	stored := storedCred{SIPUser: sipUser, Password: password, ExpiresAt: expires}
	body, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	if err := p.RDB.Set(ctx, credKey(userID), body, p.TTL).Err(); err != nil {
		return nil, fmt.Errorf("store cred: %w", err)
	}
	return p.toConfig(stored), nil
}

// Revoke removes the directory user and Redis credential.
func (p *Provisioner) Revoke(ctx context.Context, userID uuid.UUID) error {
	stored, err := p.load(ctx, userID)
	sipUser := "agent-" + userID.String()
	if err == nil && stored.SIPUser != "" {
		sipUser = stored.SIPUser
	}
	_ = os.Remove(filepath.Join(p.DirectoryDir, sipUser+".xml"))
	_, _ = p.ESL.API("reloadxml")
	_ = p.RDB.Del(ctx, credKey(userID)).Err()
	if p.Pres != nil {
		_ = p.Pres.Stop(ctx, userID)
	}
	return nil
}

// Get returns the active WebRTC config for the agent.
func (p *Provisioner) Get(ctx context.Context, userID uuid.UUID) (*Config, error) {
	stored, err := p.load(ctx, userID)
	if err != nil {
		return nil, err
	}
	if p.now().After(stored.ExpiresAt) {
		_ = p.Revoke(ctx, userID)
		return nil, ErrNoCreds
	}
	return p.toConfig(*stored), nil
}

func (p *Provisioner) load(ctx context.Context, userID uuid.UUID) (*storedCred, error) {
	raw, err := p.RDB.Get(ctx, credKey(userID)).Bytes()
	if err == redis.Nil {
		return nil, ErrNoCreds
	}
	if err != nil {
		return nil, err
	}
	var s storedCred
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (p *Provisioner) toConfig(s storedCred) *Config {
	domain := p.SIPDomain
	if domain == "" {
		domain = "localhost"
	}
	wss := p.WSSURL
	if wss == "" {
		wss = "wss://localhost:7443"
	}
	ice := p.ICEServers
	if ice == nil {
		ice = []ICEServer{}
	}
	return &Config{
		WSSURL:     wss,
		SIPURI:     fmt.Sprintf("sip:%s@%s", s.SIPUser, domain),
		Password:   s.Password,
		ICEServers: ice,
	}
}

func (p *Provisioner) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now().UTC()
}

func credKey(userID uuid.UUID) string {
	return credKeyPrefix + userID.String() + credKeySuffix
}

func randomPassword() (string, error) {
	buf := make([]byte, passwordBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (p *Provisioner) writeDirectoryUser(sipUser, password string) error {
	if err := os.MkdirAll(p.DirectoryDir, 0o755); err != nil {
		return fmt.Errorf("directory dir: %w", err)
	}
	body, err := renderDirectoryXML(sipUser, password)
	if err != nil {
		return err
	}
	path := filepath.Join(p.DirectoryDir, sipUser+".xml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write directory xml: %w", err)
	}
	return nil
}

func renderDirectoryXML(sipUser, password string) ([]byte, error) {
	s := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<include>
  <user id="%s">
    <params>
      <param name="password" value="%s"/>
      <param name="vm-password" value="%s"/>
    </params>
    <variables/>
  </user>
</include>
`, xmlEscapeAttr(sipUser), xmlEscapeAttr(password), xmlEscapeAttr(password))
	return []byte(s), nil
}

func xmlEscapeAttr(s string) string {
	r := strings.NewReplacer(`&`, "&amp;", `"`, "&quot;", `<`, "&lt;", `>`, "&gt;")
	return r.Replace(s)
}
