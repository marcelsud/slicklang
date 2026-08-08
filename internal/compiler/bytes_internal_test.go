package compiler

import "testing"

func TestStdBytesToUtf8ReturnsTypedFailure(t *testing.T) {
	program, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      "function main() -> null { null }",
	}})
	if len(diagnostics) != 0 {
		t.Fatalf("compile test program: %+v", diagnostics)
	}
	function := program.functions[string(nativeStdBytesToUtf8)]
	result, err := program.callFunction(function, []runtimeValue{{typ: "bytes", scalar: []byte{0xff}}}, nil, nil)
	if err != nil {
		t.Fatalf("convert invalid UTF-8: %v", err)
	}
	if result.typ != "Result<string,std.bytes.Utf8Failure>" || result.result == nil || result.result.ok {
		t.Fatalf("expected typed Err, found %+v", result)
	}
	failure := result.result.payload
	if failure.typ != stdBytesUtf8FailureName || formatRuntimeValue(failure.fields["Message"]) != "invalid UTF-8" {
		t.Fatalf("expected Utf8Failure payload, found %+v", failure)
	}
}
