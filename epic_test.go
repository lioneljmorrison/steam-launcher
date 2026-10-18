package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEpicApps(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("A.item", `{"DisplayName":"Game","AppName":"Sugar","CatalogNamespace":"ns","CatalogItemId":"abc","MainGameAppName":"Sugar","InstallLocation":"D:\\Games\\Sugar","AppCategories":["public","games","applications"]}`)
	write("B.item", `{"DisplayName":"Game DLC","AppName":"SugarDLC","CatalogNamespace":"ns","CatalogItemId":"def","MainGameAppName":"Sugar","AppCategories":["public","addons"]}`)
	write("C.item", `{"DisplayName":"Half done","AppName":"Half","AppCategories":["games"],"bIsIncompleteInstall":true}`)
	write("D.item", `{"DisplayName":"Unreal Engine","AppName":"UE_5.4","AppCategories":["engines"]}`)
	apps, err := epicAppsIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].ID != "epic-Sugar" || apps[0].catalogID != "abc" || apps[0].home != `D:\Games\Sugar` {
		t.Fatalf("apps = %+v", apps)
	}
	if want := "com.epicgames.launcher://apps/ns:abc:Sugar?action=launch&silent=true"; apps[0].exe != want {
		t.Errorf("launch url = %q, want %q", apps[0].exe, want)
	}
	if id, ok := parseScan("http://pc:8787/launch/epic-Sugar?k=x"); !ok || id != "epic-Sugar" {
		t.Errorf("parseScan = %q, %v", id, ok)
	}

	var it epicCatalogItem
	if err := json.Unmarshal([]byte(`{"developer":"Studio","description":"A game.","keyImages":[{"type":"Thumbnail","url":"https://x/t.jpg"},{"type":"DieselGameBoxTall","url":"https://x/tall.jpg"},{"type":"DieselGameBox","url":"https://x/wide.jpg"}]}`), &it); err != nil {
		t.Fatal(err)
	}
	d := epicDetails(it)
	if d.Developer != "Studio" || d.Art["capsule"] != "https://x/tall.jpg" || d.Art["header"] != "https://x/wide.jpg" || d.Art["logo"] != "" {
		t.Errorf("details = %+v", d)
	}
}
