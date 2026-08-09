package compiler

import "testing"

func TestRuntimeBufferPushUsesAmortizedGrowth(t *testing.T) {
	program, diagnostics := compile(nil)
	if len(diagnostics) != 0 {
		t.Fatalf("compile standard library: %+v", diagnostics)
	}
	created, err := program.callFunction(program.functions[string(nativeStdBufferNew)], nil, nil, []string{"int"})
	if err != nil {
		t.Fatalf("create buffer: %v", err)
	}
	push := program.functions[string(nativeStdBufferPush)]
	const count = 8192
	backingChanges := 0
	var previous *runtimeValue
	for index := range count {
		if _, err := program.callFunction(push, []runtimeValue{
			created,
			{typ: "int", scalar: int64(index)},
		}, nil, []string{"int"}); err != nil {
			t.Fatalf("push %d: %v", index, err)
		}
		current := &created.buffer.values[0]
		if previous != nil && current != previous {
			backingChanges++
		}
		previous = current
	}
	if len(created.buffer.values) != count {
		t.Fatalf("buffer length = %d, want %d", len(created.buffer.values), count)
	}
	if backingChanges >= count/2 {
		t.Fatalf("buffer backing changed %d times for %d pushes", backingChanges, count)
	}
}
