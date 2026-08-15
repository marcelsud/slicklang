package compiler_test

import "testing"

func TestGenericEffectsAgreeAcrossBackends(t *testing.T) {
	const source = `
function Append<T>(Target: Buffer<T>, Value: T) -> int effects { state } {
    std.buffer.Push<T>(Target, Value)
    std.buffer.Length<T>(Target)
}

function main() -> string effects { state } {
    let Values = std.buffer.New<int>()
    let First = Append<int>(Values, 20)
    let Second = Append<int>(Values, 22)
    ` + "`" + `${First}:${Second}` + "`" + `
}
`
	if output := runResultEverywhere(t, source); output != "1:2" {
		t.Fatalf("effectful generic produced %q", output)
	}
}
