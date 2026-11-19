package data

import (
	"os"
	"path/filepath"
	"testing"
)

// FEAT-9995385628: data/roadlay.json loader.

const roadClassesFixture = `{"classes":[{"id":"gravel"},{"id":"alley"}]}`

func writeRoadLayDir(t *testing.T, roadlay string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileRoadLay), []byte(roadlay), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "roads.json"), []byte(roadClassesFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadRoadLay_Good(t *testing.T) {
	rl, err := LoadRoadLay(writeRoadLayDir(t, `{"version":1,"materialsTonnes":5,"labourDays":0,"leadDays":7,"class":"alley"}`), "corr")
	if err != nil {
		t.Fatal(err)
	}
	if *rl.MaterialsTonnes != 5 || *rl.LabourDays != 0 || *rl.LeadDays != 7 || rl.Class != "alley" {
		t.Errorf("decoded %+v", rl)
	}
}

func TestLoadRoadLay_BadFilesAreRejected(t *testing.T) {
	cases := map[string]string{
		"missing tonnes":   `{"version":1,"labourDays":1,"leadDays":1,"class":"gravel"}`,
		"missing labour":   `{"version":1,"materialsTonnes":1,"leadDays":1,"class":"gravel"}`,
		"missing lead":     `{"version":1,"materialsTonnes":1,"labourDays":1,"class":"gravel"}`,
		"missing class":    `{"version":1,"materialsTonnes":1,"labourDays":1,"leadDays":1}`,
		"missing version":  `{"materialsTonnes":1,"labourDays":1,"leadDays":1,"class":"gravel"}`,
		"zero tonnes":      `{"version":1,"materialsTonnes":0,"labourDays":1,"leadDays":1,"class":"gravel"}`,
		"negative tonnes":  `{"version":1,"materialsTonnes":-1,"labourDays":1,"leadDays":1,"class":"gravel"}`,
		"negative labour":  `{"version":1,"materialsTonnes":1,"labourDays":-1,"leadDays":1,"class":"gravel"}`,
		"negative lead":    `{"version":1,"materialsTonnes":1,"labourDays":1,"leadDays":-1,"class":"gravel"}`,
		"unknown class":    `{"version":1,"materialsTonnes":1,"labourDays":1,"leadDays":1,"class":"cobbles"}`,
		"unknown field":    `{"version":1,"materialsTonnes":1,"labourDays":1,"leadDays":1,"class":"gravel","extra":1}`,
		"tonnes not whole": `{"version":1,"materialsTonnes":1.5,"labourDays":1,"leadDays":1,"class":"gravel"}`,
	}
	for name, body := range cases {
		if _, err := LoadRoadLay(writeRoadLayDir(t, body), "corr"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadRoadLay_RealFileLoads(t *testing.T) {
	dir, err := ResolveDataDir("corr")
	if err != nil {
		t.Fatal(err)
	}
	rl, err := LoadRoadLay(dir, "corr")
	if err != nil {
		t.Fatalf("the repository's data/roadlay.json does not load: %v", err)
	}
	if *rl.MaterialsTonnes <= 0 {
		t.Errorf("tonnes %d", *rl.MaterialsTonnes)
	}
}
