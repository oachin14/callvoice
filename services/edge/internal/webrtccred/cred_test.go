package webrtccred_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/callvoice/callvoice/services/edge/internal/agent"
	"github.com/callvoice/callvoice/services/edge/internal/webrtccred"
)

type fakeESL struct {
	cmds []string
}

func (f *fakeESL) API(cmd string) (string, error) {
	f.cmds = append(f.cmds, cmd)
	return "+OK", nil
}

func TestIssueWritesDirectoryXMLAndReloadxml(t *testing.T) {
	dir := t.TempDir()
	esl := &fakeESL{}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	p := &webrtccred.Provisioner{
		ESL:          esl,
		DirectoryDir: dir,
		WSSURL:       "wss://localhost:7443",
		SIPDomain:    "localhost",
		RDB:          rdb,
		TTL:          2 * time.Hour,
	}
	uid := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	cfg, err := p.Issue(context.Background(), uid)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if cfg.WSSURL != "wss://localhost:7443" {
		t.Fatalf("wssUrl = %q", cfg.WSSURL)
	}
	wantURI := "sip:agent-11111111-1111-1111-1111-111111111111@localhost"
	if cfg.SIPURI != wantURI {
		t.Fatalf("sipUri = %q, want %q", cfg.SIPURI, wantURI)
	}
	if cfg.Password == "" {
		t.Fatal("expected password")
	}
	if cfg.ICEServers == nil {
		t.Fatal("iceServers must be non-nil slice")
	}

	xmlPath := filepath.Join(dir, "agent-11111111-1111-1111-1111-111111111111.xml")
	body, err := os.ReadFile(xmlPath)
	if err != nil {
		t.Fatalf("read xml: %v", err)
	}
	xml := string(body)
	if !strings.Contains(xml, `id="agent-11111111-1111-1111-1111-111111111111"`) {
		t.Fatalf("missing user id in xml:\n%s", xml)
	}
	if !strings.Contains(xml, cfg.Password) {
		t.Fatalf("password missing from xml")
	}
	if len(esl.cmds) != 1 || esl.cmds[0] != "reloadxml" {
		t.Fatalf("expected reloadxml, got %v", esl.cmds)
	}

	got, err := p.Get(context.Background(), uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Password != cfg.Password {
		t.Fatalf("Get password mismatch")
	}
}

func TestRevokeRemovesXML(t *testing.T) {
	dir := t.TempDir()
	esl := &fakeESL{}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	pres := agent.NewPresence(rdb, agent.DefaultTTL)
	p := &webrtccred.Provisioner{
		ESL:          esl,
		DirectoryDir: dir,
		WSSURL:       "wss://localhost:7443",
		SIPDomain:    "localhost",
		RDB:          rdb,
		Pres:         pres,
	}
	uid := uuid.New()
	ctx := context.Background()
	if _, err := p.Issue(ctx, uid); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := pres.Start(ctx, uid); err != nil {
		t.Fatalf("Start presence: %v", err)
	}
	if err := p.Revoke(ctx, uid); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	path := filepath.Join(dir, "agent-"+uid.String()+".xml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("xml still present: %v", err)
	}
	if _, err := p.Get(ctx, uid); err != webrtccred.ErrNoCreds {
		t.Fatalf("Get after revoke: %v", err)
	}
	if _, err := pres.Get(ctx, uid); err != agent.ErrNotFound {
		t.Fatalf("presence after revoke: %v", err)
	}
}
