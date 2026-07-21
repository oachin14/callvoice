package fs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/callvoice/callvoice/internal/models"
	"github.com/callvoice/callvoice/services/edge/internal/fs"
)

type fakeESL struct {
	cmds []string
	err  error
}

func (f *fakeESL) API(cmd string) (string, error) {
	f.cmds = append(f.cmds, cmd)
	return "+OK", f.err
}

func TestApplyCarrierGatewayWritesXMLAndRescans(t *testing.T) {
	dir := t.TempDir()
	esl := &fakeESL{}
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	user := "trunkuser"
	realm := "sip.example.com"
	carrier := models.Carrier{
		ID:        id,
		Name:      "Primary Trunk",
		Host:      "sip.example.com",
		Port:      5060,
		Transport: "udp",
		Username:  &user,
		Realm:     &realm,
		Enabled:   true,
		Priority:  10,
		CreatedAt: time.Now().UTC(),
	}

	if err := fs.ApplyCarrierGateway(esl, dir, carrier, "s3cret"); err != nil {
		t.Fatalf("ApplyCarrierGateway: %v", err)
	}

	xmlPath := filepath.Join(dir, id.String()+".xml")
	body, err := os.ReadFile(xmlPath)
	if err != nil {
		t.Fatalf("read gateway xml: %v", err)
	}
	xml := string(body)
	for _, want := range []string{
		`name="` + id.String() + `"`,
		`name="proxy" value="sip.example.com:5060"`,
		`name="username" value="trunkuser"`,
		`name="password" value="s3cret"`,
		`name="realm" value="sip.example.com"`,
		`name="register-transport" value="udp"`,
	} {
		if !strings.Contains(xml, want) {
			t.Fatalf("gateway xml missing %q\n%s", want, xml)
		}
	}

	if len(esl.cmds) < 2 {
		t.Fatalf("expected killgw + rescan, got %v", esl.cmds)
	}
	if !strings.Contains(esl.cmds[0], "sofia profile external killgw "+id.String()) {
		t.Fatalf("expected killgw, got %q", esl.cmds[0])
	}
	if !strings.Contains(esl.cmds[1], "sofia profile external rescan") {
		t.Fatalf("expected rescan, got %q", esl.cmds[1])
	}
}

func TestReconcileGatewayDirectoryRemovesOrphans(t *testing.T) {
	dir := t.TempDir()
	esl := &fakeESL{}

	enabledID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	orphanID := uuid.MustParse("44444444-4444-4444-4444-444444444444")

	for _, id := range []uuid.UUID{enabledID, orphanID} {
		path := filepath.Join(dir, id.String()+".xml")
		if err := os.WriteFile(path, []byte("<include/>"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	enabled := map[uuid.UUID]struct{}{enabledID: {}}
	if err := fs.ReconcileGatewayDirectory(esl, dir, enabled); err != nil {
		t.Fatalf("ReconcileGatewayDirectory: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, enabledID.String()+".xml")); err != nil {
		t.Fatalf("enabled gateway xml should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, orphanID.String()+".xml")); !os.IsNotExist(err) {
		t.Fatalf("orphan gateway xml should be removed, stat err=%v", err)
	}
	if len(esl.cmds) != 1 || !strings.Contains(esl.cmds[0], "sofia profile external killgw "+orphanID.String()) {
		t.Fatalf("expected killgw for orphan only, got %v", esl.cmds)
	}
}

func TestRemoveCarrierGatewayDeletesXMLAndKillgw(t *testing.T) {
	dir := t.TempDir()
	esl := &fakeESL{}
	id := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	path := filepath.Join(dir, id.String()+".xml")
	if err := os.WriteFile(path, []byte("<include/>"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fs.RemoveCarrierGateway(esl, dir, id); err != nil {
		t.Fatalf("RemoveCarrierGateway: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected xml removed, stat err=%v", err)
	}
	if len(esl.cmds) != 1 || !strings.Contains(esl.cmds[0], "sofia profile external killgw "+id.String()) {
		t.Fatalf("expected killgw, got %v", esl.cmds)
	}
}
