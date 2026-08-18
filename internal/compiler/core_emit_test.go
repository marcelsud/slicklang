package compiler

import "testing"

func TestGoAndLLVMEmissionDependsOnlyOnCoreIR(t *testing.T) {
	sources := map[string]Source{
		"control-flow": {Name: "main.slk", Namespace: "root", Text: primitiveProgram},
		"values":       coreIRTestSource,
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			assertCoreEmitterParity(t, source)
		})
	}
}

func assertCoreEmitterParity(t *testing.T, source Source) {
	t.Helper()
	program, diagnostics := compile([]Source{source})
	requireNoDiagnostics(t, diagnostics)
	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}

	wantGo, err := program.generateGo()
	if err != nil {
		t.Fatal(err)
	}
	wantLLVM, err := program.generateLLVM()
	if err != nil {
		t.Fatal(err)
	}

	// Destroy the checked source graph before constructing either emitter input.
	program.functions = nil
	program.classes = nil
	program.interfaces = nil
	program.unions = nil
	program.constants = nil
	program.expressionTypes = nil

	projected, err := coreEmitterProgram(core)
	if err != nil {
		t.Fatal(err)
	}
	gotGo, err := projected.generateGo()
	if err != nil {
		t.Fatal(err)
	}
	gotLLVM, err := projected.generateLLVM()
	if err != nil {
		t.Fatal(err)
	}
	if gotGo != wantGo {
		t.Fatal("generated Go changed after Core-only projection")
	}
	if gotLLVM != wantLLVM {
		t.Fatal("LLVM IR changed after Core-only projection")
	}
}

func TestNativeCoreRejectsContractMismatches(t *testing.T) {
	program, diagnostics := compile([]Source{{Name: "main.slk", Namespace: "root", Text: `function main() -> int { 0 }`}})
	requireNoDiagnostics(t, diagnostics)
	core, err := program.lowerCore()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := runtimeInputsForCore(core)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*coreProgram, *backendRuntimeInputs){
		"evaluation-order": func(core *coreProgram, _ *backendRuntimeInputs) { core.EvaluationOrder = "right_to_left" },
		"cleanup":          func(core *coreProgram, _ *backendRuntimeInputs) { core.CleanupSuppression = "mutable" },
		"abi":              func(_ *coreProgram, runtime *backendRuntimeInputs) { runtime.abiVersion++ },
		"entrypoint":       func(core *coreProgram, _ *backendRuntimeInputs) { core.Functions = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			invalidCore, invalidRuntime := core, runtime
			mutate(&invalidCore, &invalidRuntime)
			if err := validateNativeCore(invalidCore, invalidRuntime); err == nil {
				t.Fatal("expected native Core validation error")
			}
		})
	}
}
