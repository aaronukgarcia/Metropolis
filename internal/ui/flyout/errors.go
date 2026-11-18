package flyout

// Registry codes (data/errors.json, range V990-V999 owned by ui.flyout).
const (
	// ErrInvalidMenu is a menu table refused at construction (bad letter,
	// empty label, empty list, depth over MaxDepth, ...). The cause names the
	// path of the offending entry.
	ErrInvalidMenu = "MET-V990"
	// ErrMenuNotBuilt is a method call on a Menu that was not built with New
	// from a valid table (the zero value, or a nil pointer).
	ErrMenuNotBuilt = "MET-V991"
)
