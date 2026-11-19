package data

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-9995385628 (roads lane 1a): data/roadlay.json is the economics of the
// road a player lays one cell at a time: tonnes of construction materials,
// labour days, lead days and the class id (a row of data/roads.json). It
// lives in its own file, not in roads.json, because engine.roads is unwired
// and owns that file's schema. Every number is a BALANCE PLACEHOLDER for
// Aaron's row-by-row pass (GR#15: never a Go literal).

// FileRoadLay is data/roadlay.json's filename.
const FileRoadLay = "roadlay.json"

// fileRoadClasses is the class ladder the road class must name a row of.
const fileRoadClasses = "roads.json"

// RoadLay is data/roadlay.json's schema. Every field except Comment is
// required: a pointer tells "absent" from a legitimate zero.
type RoadLay struct {
	Version         int    `json:"version"`
	Comment         string `json:"$comment,omitempty"`
	MaterialsTonnes *int64 `json:"materialsTonnes"`
	LabourDays      *int64 `json:"labourDays"`
	LeadDays        *int64 `json:"leadDays"`
	Class           string `json:"class"`
}

// Validate implements Validator.
func (r *RoadLay) Validate() error {
	if err := requireVersion(r.Version); err != nil {
		return err
	}
	if r.MaterialsTonnes == nil {
		return fieldErr("materialsTonnes", "required")
	}
	if *r.MaterialsTonnes <= 0 {
		return fieldErr("materialsTonnes", "must be > 0")
	}
	if r.LabourDays == nil {
		return fieldErr("labourDays", "required")
	}
	if *r.LabourDays < 0 {
		return fieldErr("labourDays", "must be >= 0")
	}
	if r.LeadDays == nil {
		return fieldErr("leadDays", "required")
	}
	if *r.LeadDays < 0 {
		return fieldErr("leadDays", "must be >= 0")
	}
	if r.Class == "" {
		return fieldErr("class", "required")
	}
	return nil
}

// LoadRoadLay loads and validates roadlay.json from dir, and checks that its
// class names a row of roads.json in the same dir. A bad file fails with a
// registry code (MET-F601 file missing, MET-F6xx schema), never a default.
func LoadRoadLay(dir, correlationID string) (RoadLay, error) {
	rl, err := Load[RoadLay, *RoadLay](filepath.Join(dir, FileRoadLay), correlationID)
	if err != nil {
		return RoadLay{}, err
	}
	classPath := filepath.Join(dir, fileRoadClasses)
	raw, err := os.ReadFile(classPath)
	if err != nil {
		return RoadLay{}, errs.Wrap(CodeFileNotFound, correlationID, err, map[string]any{"path": classPath})
	}
	var ladder struct {
		Classes []struct {
			ID string `json:"id"`
		} `json:"classes"`
	}
	if err := json.Unmarshal(raw, &ladder); err != nil {
		return RoadLay{}, errs.Wrap(CodeMalformedJSON, correlationID, err, map[string]any{"path": classPath})
	}
	for _, c := range ladder.Classes {
		if c.ID == rl.Class {
			return rl, nil
		}
	}
	return RoadLay{}, errs.Wrap(CodeSchemaInvalid, correlationID, fieldErr("class", "not a class id in "+fileRoadClasses), map[string]any{
		"path": filepath.Join(dir, FileRoadLay), "field": "class", "rule": "not a class id in " + fileRoadClasses,
	})
}
