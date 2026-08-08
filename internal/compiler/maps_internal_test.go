package compiler

import "testing"

func TestRuntimeMapUpdatesPreserveEarlierValues(t *testing.T) {
	key := func(value string) runtimeValue { return runtimeValue{typ: "string", scalar: value} }
	integer := func(value int64) runtimeValue { return runtimeValue{typ: "int", scalar: value} }
	original, err := newRuntimeMap([]runtimeMapEntry{
		{key: key("Ada"), value: integer(1)},
		{key: key("Ada"), value: integer(2)},
		{key: key("Grace"), value: integer(3)},
	})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	if len(original.entries) != 2 || original.entries[0].value.scalar != int64(2) || original.entries[0].key.scalar != "Ada" {
		t.Fatalf("dynamic collision changed order or value: %+v", original.entries)
	}

	same, err := runtimeMapWith(original, key("Ada"), integer(2))
	if err != nil || same != original {
		t.Fatalf("same-value With copied the map: %v", err)
	}
	updated, err := runtimeMapWith(original, key("Ada"), integer(4))
	if err != nil {
		t.Fatalf("update map: %v", err)
	}
	if updated == original || original.entries[0].value.scalar != int64(2) || updated.entries[0].value.scalar != int64(4) {
		t.Fatalf("With mutated its input")
	}
	unchanged, err := runtimeMapWithout(original, key("missing"))
	if err != nil || unchanged != original {
		t.Fatalf("absent Without copied the map: %v", err)
	}
	removed, err := runtimeMapWithout(updated, key("Ada"))
	if err != nil {
		t.Fatalf("remove map key: %v", err)
	}
	if removed == updated || len(removed.entries) != 1 || removed.entries[0].key.scalar != "Grace" || len(updated.entries) != 2 {
		t.Fatalf("Without mutated its input or order: %+v", removed.entries)
	}
}
