package compiler

import "testing"

func TestStdHTTPServerRegistrySurface(t *testing.T) {
	program := newProgram()
	registerStandardLibrary(program)

	assertStandardFunction(t, program.functions[string(nativeStdHTTPServerServe)], "std.http.server", "Serve",
		[]string{stdHTTPServerConfigName, stdHTTPServerHandlerName},
		"Result<null,"+stdHTTPServerFailureName+">",
		nativeStdHTTPServerServe,
	)

	handler := program.interfaces[stdHTTPServerHandlerName]
	if handler == nil || handler.namespace != "std.http.server" || handler.name != "Handler" {
		t.Fatalf("Handler interface = %+v", handler)
	}
	if method := handler.methods["Handle"]; method == nil || method.result.name != stdHTTPServerResponseName {
		t.Fatalf("Handle method = %+v", method)
	}

	config := program.classes[stdHTTPServerConfigName]
	if config == nil || config.fields["Address"].typ.name != "string" {
		t.Fatalf("Config = %+v", config)
	}
	failure := program.classes[stdHTTPServerFailureName]
	if failure == nil || !failure.isError {
		t.Fatal("std.http.server.Failure must implement Error")
	}
	if documentation := program.namespaceDocumentation["std.http.server"]; documentation == nil || *documentation == "" {
		t.Fatal("std.http.server namespace documentation missing")
	}
}
