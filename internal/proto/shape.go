package proto

// Shape helpers: version-dependent field presence derived from the protocol
// number, so the session core stays free of magic numbers. Thresholds are
// documented where known and conservative otherwise.

// SetDefaultSpawnHasDimension reports whether Set Default Spawn Position
// carries a leading dimension name (added in 26.2 / protocol 776).
func (t Tables) SetDefaultSpawnHasDimension() bool { return t.Protocol >= 776 }

// SetDefaultSpawnHasPitch reports whether that packet also carries a trailing
// pitch, added alongside the dimension name.
func (t Tables) SetDefaultSpawnHasPitch() bool { return t.Protocol >= 776 }
