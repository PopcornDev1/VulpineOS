package vault

import (
	"os"
	"testing"
)

func TestProxyCRUD(t *testing.T) {
	f, err := os.CreateTemp("", "vault-proxy-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	db, err := OpenPath(f.Name())
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer db.Close()

	// Add proxy
	p, err := db.AddProxy(`{"type":"http","host":"1.2.3.4","port":8080}`, `{"ip":"1.2.3.4"}`, "US Proxy")
	if err != nil {
		t.Fatalf("add proxy: %v", err)
	}
	if p.Label != "US Proxy" {
		t.Errorf("label = %q, want 'US Proxy'", p.Label)
	}
	if p.ID == "" {
		t.Fatal("expected non-empty ID")
	}

	// Add another
	_, err = db.AddProxy(`{"type":"socks5","host":"10.0.0.1","port":1080}`, "", "EU Proxy")
	if err != nil {
		t.Fatalf("add second proxy: %v", err)
	}

	// List proxies
	proxies, err := db.ListProxies()
	if err != nil {
		t.Fatalf("list proxies: %v", err)
	}
	if len(proxies) != 2 {
		t.Fatalf("expected 2 proxies, got %d", len(proxies))
	}

	// Get proxy
	got, err := db.GetProxy(p.ID)
	if err != nil {
		t.Fatalf("get proxy: %v", err)
	}
	if got.Config != p.Config {
		t.Errorf("config = %q, want %q", got.Config, p.Config)
	}

	// Update geo
	newGeo := `{"ip":"1.2.3.4","timezone":"America/New_York"}`
	if err := db.UpdateProxyGeo(p.ID, newGeo); err != nil {
		t.Fatalf("update geo: %v", err)
	}
	got, _ = db.GetProxy(p.ID)
	if got.Geo != newGeo {
		t.Errorf("geo = %q, want %q", got.Geo, newGeo)
	}

	// Delete proxy
	if err := db.DeleteProxy(p.ID); err != nil {
		t.Fatalf("delete proxy: %v", err)
	}
	proxies, _ = db.ListProxies()
	if len(proxies) != 1 {
		t.Fatalf("expected 1 proxy after delete, got %d", len(proxies))
	}

	// Delete non-existent
	err = db.DeleteProxy("nonexistent-id")
	if err == nil {
		t.Fatal("expected error deleting non-existent proxy")
	}
}

func TestProxyUpdateGeo(t *testing.T) {
	f, err := os.CreateTemp("", "vault-proxy-geo-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	db, err := OpenPath(f.Name())
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer db.Close()

	// Add proxy with no geo
	p, err := db.AddProxy(`{"type":"http","host":"5.6.7.8","port":3128}`, "", "Test Proxy")
	if err != nil {
		t.Fatalf("add proxy: %v", err)
	}
	if p.Geo != "" {
		t.Errorf("initial geo should be empty, got %q", p.Geo)
	}

	// Update geo
	geoJSON := `{"ip":"5.6.7.8","timezone":"Europe/London","lat":51.5,"lon":-0.12,"country":"United Kingdom"}`
	if err := db.UpdateProxyGeo(p.ID, geoJSON); err != nil {
		t.Fatalf("update geo: %v", err)
	}

	// Verify geo is stored
	got, err := db.GetProxy(p.ID)
	if err != nil {
		t.Fatalf("get proxy: %v", err)
	}
	if got.Geo != geoJSON {
		t.Errorf("geo = %q, want %q", got.Geo, geoJSON)
	}

	// Update geo on non-existent proxy should fail
	err = db.UpdateProxyGeo("nonexistent-id", `{"ip":"0.0.0.0"}`)
	if err == nil {
		t.Fatal("expected error updating geo on non-existent proxy")
	}
}

func TestProxyListOrder(t *testing.T) {
	f, err := os.CreateTemp("", "vault-proxy-order-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	db, err := OpenPath(f.Name())
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer db.Close()

	// Add 3 proxies
	p1, err := db.AddProxy(`{"type":"http","host":"1.1.1.1","port":80}`, "", "Proxy A")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := db.AddProxy(`{"type":"socks5","host":"2.2.2.2","port":1080}`, "", "Proxy B")
	if err != nil {
		t.Fatal(err)
	}
	p3, err := db.AddProxy(`{"type":"http","host":"3.3.3.3","port":8080}`, "", "Proxy C")
	if err != nil {
		t.Fatal(err)
	}

	proxies, err := db.ListProxies()
	if err != nil {
		t.Fatalf("list proxies: %v", err)
	}
	if len(proxies) != 3 {
		t.Fatalf("expected 3 proxies, got %d", len(proxies))
	}

	// Verify all proxies are present
	ids := map[string]bool{p1.ID: false, p2.ID: false, p3.ID: false}
	for _, p := range proxies {
		ids[p.ID] = true
	}
	for id, found := range ids {
		if !found {
			t.Errorf("proxy %s not found in list", id)
		}
	}

	// Verify labels are all present
	labels := map[string]bool{}
	for _, p := range proxies {
		labels[p.Label] = true
	}
	for _, want := range []string{"Proxy A", "Proxy B", "Proxy C"} {
		if !labels[want] {
			t.Errorf("missing proxy with label %q", want)
		}
	}
}
