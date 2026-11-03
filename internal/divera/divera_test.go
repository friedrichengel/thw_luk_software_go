package divera_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/divera"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

func TestSanitize(t *testing.T) {
	in := `{"success":true,"data":{"user":{"accesskey":"GEHEIM","firstname":"Max","lastname":"Muster","stdformat_name":"Max Muster"},
	"cluster":{"consumer":[{"id":1,"firstname":"erika","lastname":"Ölmann","stdformat_name":"Erika Ölmann","qualification":[1,2]},{"id":2,"firstname":"","lastname":"Nur Nachname","stdformat_name":"Nur Nachname"}]},
	"monitor":{"2":{"9868":{"all":[{"firstname":"Tim","lastname":"Test"}]}}}}}`
	out := divera.Sanitize([]byte(in))
	s := string(out)
	for _, forbidden := range []string{"GEHEIM", "Muster", "Ölmann", "Nachname", "\"Tim\"", "Erika"} {
		if strings.Contains(s, forbidden) {
			t.Fatalf("%q nicht gekuerzt: %s", forbidden, s)
		}
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	data := v["data"].(map[string]any)
	u := data["user"].(map[string]any)
	if u["accesskey"] != "123456" || u["firstname"] != "M." || u["lastname"] != "M." || u["stdformat_name"] != "M. M." {
		t.Fatalf("user: %+v", u)
	}
	cons := data["cluster"].(map[string]any)["consumer"].([]any)
	c0 := cons[0].(map[string]any)
	if c0["firstname"] != "E." || c0["lastname"] != "Ö." || c0["stdformat_name"] != "E. Ö." {
		t.Fatalf("consumer: %+v", c0)
	}
	if c1 := cons[1].(map[string]any); c1["stdformat_name"] != "N." || c1["firstname"] != "" {
		t.Fatalf("consumer ohne Vorname: %+v", c1)
	}
	// ungueltiges JSON bleibt unveraendert
	if string(divera.Sanitize([]byte("nope"))) != "nope" {
		t.Fatal("ungueltiges JSON veraendert")
	}
}

// FilterGroups entfernt ausgeblendete Gruppen aus cluster.group und aus den
// Gruppenlisten der Personen; parseGroups liest Objekt- und Listenform.
func TestFilterGroups(t *testing.T) {
	in := `{"success":true,"data":{"cluster":{"group":{"1":{"name":"Zugtrupp","sorting":2},"2":{"name":"Alle","sorting":1},"3":{"id":3,"name":"Bergung","sorting":3}},
	"consumer":{"10":{"firstname":"A","groups":[1,2]},"11":{"firstname":"B","groups":[2,3]}}}}}`
	out := divera.FilterGroups([]byte(in), []int64{2})
	s := string(out)
	if strings.Contains(s, `"Alle"`) || !strings.Contains(s, `"Zugtrupp"`) || !strings.Contains(s, `"Bergung"`) {
		t.Fatalf("gruppe nicht entfernt: %s", s)
	}
	if !strings.Contains(s, `"groups":[1]`) || !strings.Contains(s, `"groups":[3]`) {
		t.Fatalf("gruppenlisten der personen nicht bereinigt: %s", s)
	}
	if string(divera.FilterGroups([]byte(in), nil)) != in {
		t.Fatal("ohne ausgeblendete Gruppen muss der Body unveraendert bleiben")
	}
	if string(divera.FilterGroups([]byte("nope"), []int64{2})) != "nope" {
		t.Fatal("ungueltiges JSON veraendert")
	}
}

// Resolve: Recht und Zugang der Organisation muessen zusammenkommen.
func TestResolve(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"), luk.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	oid, _ := org.Create(d, "OV", "", "ov")
	uid, _ := user.Create(d, "u", "geheim1234", nil, nil, "user", false)
	user.SetOrg(d, uid, &oid)
	load := func() *user.User { u, _ := user.FindByID(d, uid); return u }

	if src, _ := divera.Resolve(d, load(), "s"); src != nil {
		t.Fatal("ohne Recht darf es keinen Zugang geben")
	}
	user.SetDiveraAccess(d, uid, "read")
	if src, _ := divera.Resolve(d, load(), "s"); src != nil {
		t.Fatal("ohne Schluessel darf es keinen Zugang geben")
	}
	org.SetCredential(d, oid, org.KindDiveraAccess, "ACCESS-KEY-1234", uid, "s")
	src, err := divera.Resolve(d, load(), "s")
	if err != nil || src == nil || src.AccessKey != "ACCESS-KEY-1234" || src.SystemKey != "ACCESS-KEY-1234" || src.Write {
		t.Fatalf("read: %v %+v", err, src)
	}
	org.SetCredential(d, oid, org.KindDiveraSystem, "SYSTEM-KEY-1234", uid, "s")
	user.SetDiveraAccess(d, uid, "write")
	src, _ = divera.Resolve(d, load(), "s")
	if src == nil || src.SystemKey != "SYSTEM-KEY-1234" || !src.Write {
		t.Fatalf("write/system: %+v", src)
	}
	cfg := divera.ClientConfig(src)
	if cfg["enabled"] != true || cfg["write"] != true {
		t.Fatalf("client config: %+v", cfg)
	}
	// Admin-Konto: nie
	aid, _ := user.Create(d, "a", "adminpass123", nil, nil, "admin", false)
	user.SetDiveraAccess(d, aid, "write")
	au, _ := user.FindByID(d, aid)
	if src, _ := divera.Resolve(d, au, "s"); src != nil {
		t.Fatal("admin darf keinen Divera-Zugang haben")
	}
}
