package compiler

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type diagnosticCode string

type DiagnosticSeverity string

const (
	// DiagnosticSeverityError marks a fact that makes a program invalid.
	DiagnosticSeverityError DiagnosticSeverity = "error"
	// DiagnosticSeverityWarning marks a fact about a valid program: the source
	// compiles, and an explicit lint or quality gate still rejects it.
	DiagnosticSeverityWarning DiagnosticSeverity = "warning"
)

type DiagnosticPhase string

const (
	DiagnosticPhaseParse           DiagnosticPhase = "parse"
	DiagnosticPhaseNameResolution  DiagnosticPhase = "name-resolution"
	DiagnosticPhaseTypeCheck       DiagnosticPhase = "type-check"
	DiagnosticPhaseEffectCheck     DiagnosticPhase = "effect-check"
	DiagnosticPhaseMethodCheck     DiagnosticPhase = "method-check"
	DiagnosticPhaseVisibilityCheck DiagnosticPhase = "visibility-check"
	DiagnosticPhaseResourceCheck   DiagnosticPhase = "resource-check"
	DiagnosticPhaseDocumentation   DiagnosticPhase = "documentation"
	DiagnosticPhaseLint            DiagnosticPhase = "lint"
	DiagnosticPhaseQuality         DiagnosticPhase = "quality"
)

const (
	diagnosticCodeSyntax                   diagnosticCode = "SLK001"
	diagnosticCodeNamespace                diagnosticCode = "SLK100"
	diagnosticCodeErrorValue               diagnosticCode = "SLK200"
	diagnosticCodeUnhandledError           diagnosticCode = "SLK201"
	diagnosticCodeNonExhaustiveCatch       diagnosticCode = "SLK202"
	diagnosticCodeUnknownCallable          diagnosticCode = "SLK203"
	diagnosticCodeAlias                    diagnosticCode = "SLK204"
	diagnosticCodeUnknownClass             diagnosticCode = "SLK205"
	diagnosticCodeEffectDeclaration        diagnosticCode = "SLK206"
	diagnosticCodeUndeclaredEffect         diagnosticCode = "SLK207"
	diagnosticCodeMissingMethod            diagnosticCode = "SLK310"
	diagnosticCodeDuplicateMethod          diagnosticCode = "SLK311"
	diagnosticCodeMethodSignature          diagnosticCode = "SLK312"
	diagnosticCodeDetachedMethod           diagnosticCode = "SLK313"
	diagnosticCodeMethodReceiver           diagnosticCode = "SLK314"
	diagnosticCodeAliasedMethodReceiver    diagnosticCode = "SLK315"
	diagnosticCodeCallArgument             diagnosticCode = "SLK320"
	diagnosticCodeUnknownMethod            diagnosticCode = "SLK321"
	diagnosticCodeUnknownField             diagnosticCode = "SLK322"
	diagnosticCodePrivateAccess            diagnosticCode = "SLK330"
	diagnosticCodeReturnType               diagnosticCode = "SLK340"
	diagnosticCodeUnknownValue             diagnosticCode = "SLK341"
	diagnosticCodeTypeMismatch             diagnosticCode = "SLK342"
	diagnosticCodeNotIterable              diagnosticCode = "SLK344"
	diagnosticCodeLoopControl              diagnosticCode = "SLK345"
	diagnosticCodeLoopBindings             diagnosticCode = "SLK346"
	diagnosticCodeResultPayload            diagnosticCode = "SLK350"
	diagnosticCodeResultTypeUnknown        diagnosticCode = "SLK351"
	diagnosticCodePropagateValue           diagnosticCode = "SLK352"
	diagnosticCodePropagateReturn          diagnosticCode = "SLK353"
	diagnosticCodePropagateError           diagnosticCode = "SLK354"
	diagnosticCodeMatchValue               diagnosticCode = "SLK355"
	diagnosticCodeMatchExhaustiveness      diagnosticCode = "SLK356"
	diagnosticCodeMatchArm                 diagnosticCode = "SLK357"
	diagnosticCodeMatchArmType             diagnosticCode = "SLK358"
	diagnosticCodeResultConstructorArity   diagnosticCode = "SLK359"
	diagnosticCodeMatchPattern             diagnosticCode = "SLK360"
	diagnosticCodeGenericType              diagnosticCode = "SLK361"
	diagnosticCodeOptionalReceiver         diagnosticCode = "SLK370"
	diagnosticCodeNullTarget               diagnosticCode = "SLK371"
	diagnosticCodeOptionalValue            diagnosticCode = "SLK372"
	diagnosticCodeRedundantOptional        diagnosticCode = "SLK373"
	diagnosticCodeOptionalComparison       diagnosticCode = "SLK374"
	diagnosticCodeOptionalCondition        diagnosticCode = "SLK375"
	diagnosticCodeRequiredField            diagnosticCode = "SLK376"
	diagnosticCodeTypeArguments            diagnosticCode = "SLK380"
	diagnosticCodeJSONType                 diagnosticCode = "SLK381"
	diagnosticCodeMapTypeUnknown           diagnosticCode = "SLK382"
	diagnosticCodeMapKeyType               diagnosticCode = "SLK383"
	diagnosticCodeDuplicateMapKey          diagnosticCode = "SLK384"
	diagnosticCodeCloseMethod              diagnosticCode = "SLK385"
	diagnosticCodeCloseParameters          diagnosticCode = "SLK386"
	diagnosticCodeCloseResult              diagnosticCode = "SLK387"
	diagnosticCodeManualClose              diagnosticCode = "SLK388"
	diagnosticCodeUsingEscape              diagnosticCode = "SLK389"
	diagnosticCodeUsingAssignment          diagnosticCode = "SLK390"
	diagnosticCodeOrphanDocumentation      diagnosticCode = "SLK391"
	diagnosticCodeConflictingDocumentation diagnosticCode = "SLK392"
	diagnosticCodeResourceRequiresUsing    diagnosticCode = "SLK393"
	diagnosticCodeAsyncInitializer         diagnosticCode = "SLK394"
	diagnosticCodePendingUse               diagnosticCode = "SLK395"
	diagnosticCodePendingAssignment        diagnosticCode = "SLK396"
	diagnosticCodeAwaitUnknown             diagnosticCode = "SLK397"
	diagnosticCodeAwaitOrdinary            diagnosticCode = "SLK398"
	diagnosticCodeAwaitTwice               diagnosticCode = "SLK399"
	diagnosticCodePendingMissingAwait      diagnosticCode = "SLK400"
	diagnosticCodePendingPath              diagnosticCode = "SLK401"
	diagnosticCodeAwaitLoop                diagnosticCode = "SLK402"
	diagnosticCodeAsyncUnsafeValue         diagnosticCode = "SLK403"
	diagnosticCodeUnionVariant             diagnosticCode = "SLK404"
	diagnosticCodeConstantType             diagnosticCode = "SLK405"
	diagnosticCodeConstantExpression       diagnosticCode = "SLK406"
	diagnosticCodeConstantAssignment       diagnosticCode = "SLK407"
	diagnosticCodeConstantCycle            diagnosticCode = "SLK408"
	diagnosticCodeTypeParameter            diagnosticCode = "SLK409"
	diagnosticCodeUnboundTypeParameter     diagnosticCode = "SLK410"
	diagnosticCodeGenericExpansion         diagnosticCode = "SLK411"
	diagnosticCodeNotCallable              diagnosticCode = "SLK412"
	diagnosticCodeMethodValue              diagnosticCode = "SLK413"
	diagnosticCodeLambdaCapture            diagnosticCode = "SLK414"
	diagnosticCodeAnnotation               diagnosticCode = "SLK415"
	diagnosticCodeAnnotationTarget         diagnosticCode = "SLK416"
	diagnosticCodeAnnotationArgument       diagnosticCode = "SLK417"
	diagnosticCodeAnnotationCycle          diagnosticCode = "SLK418"
	diagnosticCodeUnwrapValue              diagnosticCode = "SLK419"
	diagnosticCodeUnreadBinding            diagnosticCode = "SLK500"
	diagnosticCodeDiscardedExpression      diagnosticCode = "SLK501"
	diagnosticCodeUnreachableStatement     diagnosticCode = "SLK502"
	diagnosticCodeCyclomaticComplexity     diagnosticCode = "SLK503"
	diagnosticCodeCognitiveComplexity      diagnosticCode = "SLK504"
)

var ErrUnknownDiagnostic = errors.New("unknown diagnostic")

type DiagnosticDescription struct {
	Code           string             `json:"code"`
	Severity       DiagnosticSeverity `json:"severity"`
	Phase          DiagnosticPhase    `json:"phase"`
	Title          string             `json:"title"`
	Explanation    string             `json:"explanation"`
	Trigger        string             `json:"trigger"`
	Fixes          []string           `json:"fixes"`
	InvalidExample *string            `json:"invalid_example"`
	ValidExample   *string            `json:"valid_example"`
	Related        []string           `json:"related"`
}

type diagnosticDefinition struct {
	Code           diagnosticCode
	Severity       DiagnosticSeverity
	Phase          DiagnosticPhase
	Title          string
	Explanation    string
	Trigger        string
	Fixes          []string
	InvalidExample string
	ValidExample   string
	Related        []diagnosticCode
}

func defineDiagnostic(code diagnosticCode, phase DiagnosticPhase, title, explanation, trigger string, fixes ...string) diagnosticDefinition {
	return diagnosticDefinition{
		Code:        code,
		Severity:    DiagnosticSeverityError,
		Phase:       phase,
		Title:       title,
		Explanation: explanation,
		Trigger:     trigger,
		Fixes:       fixes,
	}
}

// asWarning marks a definition reported about a valid program. A warning never
// blocks compilation; the lint and quality gates decide what to do with it.
func (definition diagnosticDefinition) asWarning() diagnosticDefinition {
	definition.Severity = DiagnosticSeverityWarning
	return definition
}

func (definition diagnosticDefinition) withExamples(invalid, valid string) diagnosticDefinition {
	definition.InvalidExample = invalid
	definition.ValidExample = valid
	return definition
}

func (definition diagnosticDefinition) withRelated(codes ...diagnosticCode) diagnosticDefinition {
	definition.Related = codes
	return definition
}

var diagnosticDefinitions = []diagnosticDefinition{
	defineDiagnostic(diagnosticCodeSyntax, DiagnosticPhaseParse,
		"Invalid syntax",
		"The source must follow Slick's declaration and expression grammar before it can be checked.",
		"The scanner or parser encounters a malformed token or an unexpected token sequence.",
		"Correct the syntax at the reported location, using the expected token named by the diagnostic."),
	defineDiagnostic(diagnosticCodeNamespace, DiagnosticPhaseParse,
		"Invalid namespace",
		"Every source file must belong to a namespace made from valid Slick identifiers.",
		"A source is compiled with an empty, malformed, or reserved namespace name.",
		"Rename or move the source so its namespace is a dot-separated sequence of valid identifiers."),
	defineDiagnostic(diagnosticCodeErrorValue, DiagnosticPhaseEffectCheck,
		"Value is not an error",
		"Checked error operations accept only classes that implement Slick's Error interface.",
		"A throw, catch, or throws declaration names a value or type that is not an Error.",
		"Use an Error class, or make the intended class implement Error."),
	defineDiagnostic(diagnosticCodeUnhandledError, DiagnosticPhaseEffectCheck,
		"Checked error is unhandled",
		"A function must catch every checked error produced by its body or declare that error in its throws set.",
		"A call or throw can produce an error absent from the enclosing function's handled or declared effects.",
		"Catch the error before it leaves the function.",
		"Add the error type to the function's throws declaration."),
	defineDiagnostic(diagnosticCodeNonExhaustiveCatch, DiagnosticPhaseEffectCheck,
		"Catch is not exhaustive",
		"A catch expression must handle every checked error that its protected expression can produce.",
		"At least one possible checked error has no matching catch arm.",
		"Add an arm for every missing error type named by the diagnostic."),
	defineDiagnostic(diagnosticCodeUnknownCallable, DiagnosticPhaseNameResolution,
		"Unknown function or method",
		"Every call target must resolve exactly to a visible function or method.",
		"A call names no function or method in the current namespace, aliases, or receiver type.",
		"Correct the callable name or import its namespace.",
		"Declare the missing function or method before calling it."),
	defineDiagnostic(diagnosticCodeAlias, DiagnosticPhaseNameResolution,
		"Invalid alias",
		"An alias must name an existing declaration and must not conflict with a declaration in its local namespace.",
		"An alias target is missing or the alias would reuse an occupied local name.",
		"Point the alias at an existing declaration and choose an unoccupied local name."),
	defineDiagnostic(diagnosticCodeUnknownClass, DiagnosticPhaseNameResolution,
		"Unknown class",
		"Object construction requires a class declaration that is visible from the current namespace.",
		"An object expression names no known class.",
		"Correct the class name or import the namespace that declares it."),
	defineDiagnostic(diagnosticCodeEffectDeclaration, DiagnosticPhaseEffectCheck,
		"Effect declaration is invalid",
		"An effects clause contains a non-empty set of known compiler-owned effect names, with each name appearing once.",
		"An effects clause names an unknown effect or repeats one effect.",
		"Use one or more of database, environment, filesystem, io, network, process, random, state, and time.",
		"Remove duplicate effect names."),
	defineDiagnostic(diagnosticCodeUndeclaredEffect, DiagnosticPhaseEffectCheck,
		"Effect is not declared",
		"A callable must declare every observable authority effect that can occur through its body, callees, callbacks, or automatic resource cleanup.",
		"An operation requires an effect absent from the enclosing callable's effects set.",
		"Add the named effect to the callable's effects clause.",
		"Move the operation behind a callable whose explicit contract owns that effect."),
	defineDiagnostic(diagnosticCodeMissingMethod, DiagnosticPhaseMethodCheck,
		"Method implementation is missing",
		"Every method declared by a concrete class must have exactly one implementation.",
		"A class method declaration has no inline or detached implementation.",
		"Implement the method with the declared signature, or remove the declaration."),
	defineDiagnostic(diagnosticCodeDuplicateMethod, DiagnosticPhaseMethodCheck,
		"Method is implemented more than once",
		"A concrete class method has exactly one implementation.",
		"Two inline or detached implementations target the same class method.",
		"Keep one implementation and remove or rename the duplicate."),
	defineDiagnostic(diagnosticCodeMethodSignature, DiagnosticPhaseMethodCheck,
		"Method implementation has the wrong signature",
		"A method implementation must preserve its declaration's parameters, result type, and checked effects.",
		"An implementation differs from the method contract in at least one signature component.",
		"Change the implementation signature to match the declaration exactly."),
	defineDiagnostic(diagnosticCodeDetachedMethod, DiagnosticPhaseMethodCheck,
		"Detached method is not allowed",
		"A class's extension policy controls where detached method implementations may be declared.",
		"A detached implementation is declared outside the location permitted by its class.",
		"Move the implementation into the class or an allowed namespace.",
		"Change the class extension policy when external implementations are intentional."),
	defineDiagnostic(diagnosticCodeMethodReceiver, DiagnosticPhaseMethodCheck,
		"Invalid method receiver",
		"A method implementation must target a method declared by a concrete class.",
		"The receiver is not a class or the named method is absent from that class.",
		"Use the declaring class as the receiver and implement one of its declared methods."),
	defineDiagnostic(diagnosticCodeAliasedMethodReceiver, DiagnosticPhaseMethodCheck,
		"Method receiver cannot be an alias",
		"Detached method receivers use a local or absolute class name so implementation ownership is unambiguous.",
		"A detached method implementation names its receiver through an alias.",
		"Replace the alias with the class's local or fully qualified name."),
	defineDiagnostic(diagnosticCodeCallArgument, DiagnosticPhaseTypeCheck,
		"Call arguments do not match",
		"A call must supply the declared number of arguments and each argument must satisfy its parameter type.",
		"A function, method, or builtin call has the wrong arity or an incompatible argument.",
		"Supply exactly the declared arguments with values assignable to their parameter types."),
	defineDiagnostic(diagnosticCodeUnknownMethod, DiagnosticPhaseNameResolution,
		"Unknown method",
		"A method call must name a method declared for the receiver's type.",
		"The receiver type has no method with the requested name.",
		"Correct the method name or declare and implement that method on the receiver class."),
	defineDiagnostic(diagnosticCodeUnknownField, DiagnosticPhaseNameResolution,
		"Unknown field",
		"A field read or object field initializer must name a field declared by the class.",
		"The selected class has no field with the requested name.",
		"Correct the field name or add the field to the class declaration."),
	defineDiagnostic(diagnosticCodePrivateAccess, DiagnosticPhaseVisibilityCheck,
		"Declaration is private",
		"Lowercase declarations are visible only inside their owning namespace.",
		"Code in another namespace accesses a private class, interface, function, field, or method.",
		"Access the declaration from its owning namespace.",
		"Capitalize the declaration name when it is intended to be public."),
	defineDiagnostic(diagnosticCodeReturnType, DiagnosticPhaseTypeCheck,
		"Function result does not match",
		"Every reachable function result must be assignable to the function's declared return type.",
		"A function body or return statement produces an incompatible type.",
		"Return a value of the declared type, or change the return type to the intended contract."),
	defineDiagnostic(diagnosticCodeUnknownValue, DiagnosticPhaseNameResolution,
		"Unknown value",
		"Every value reference and assignment target must resolve to a local, parameter, function, or visible declaration.",
		"An expression or assignment names a value that is not in scope.",
		"Correct the name or declare the value before using it."),
	defineDiagnostic(diagnosticCodeTypeMismatch, DiagnosticPhaseTypeCheck,
		"Types are incompatible",
		"Values combined, compared, stored, or returned together must satisfy the operation's type contract.",
		"An operation receives types that cannot be joined or assigned as required.",
		"Convert or replace the value so the participating types agree.",
		"Change the receiving declaration's type when the broader type is intentional."),
	defineDiagnostic(diagnosticCodeNotIterable, DiagnosticPhaseTypeCheck,
		"Value is not iterable",
		"A for loop and iterable builtin require a value with a known iterable element type.",
		"A loop, enumerate, or zip operand is not iterable.",
		"Pass an array, map, range, or other iterable value."),
	defineDiagnostic(diagnosticCodeLoopControl, DiagnosticPhaseTypeCheck,
		"Loop control is outside a loop",
		"Break and continue affect the nearest enclosing loop and are invalid without one.",
		"A break or continue statement appears outside a loop body.",
		"Move the statement into a loop or replace it with control flow valid in the current scope."),
	defineDiagnostic(diagnosticCodeLoopBindings, DiagnosticPhaseTypeCheck,
		"Loop binding count does not match",
		"A for loop must bind exactly the number of values produced by each iterable element.",
		"The loop declares a different number of bindings than the iterable yields.",
		"Add or remove loop bindings to match the iterable element shape."),
	defineDiagnostic(diagnosticCodeResultPayload, DiagnosticPhaseTypeCheck,
		"Result payload has the wrong type",
		"Ok and Err payloads must match the success and failure types of their contextual Result<T, E>.",
		"A Result constructor receives a payload incompatible with its expected variant type.",
		"Construct the variant with a value of the expected payload type."),
	defineDiagnostic(diagnosticCodeResultTypeUnknown, DiagnosticPhaseTypeCheck,
		"Result type cannot be inferred",
		"An Ok or Err expression needs a surrounding Result<T, E> type to determine both payload types.",
		"A Result constructor appears where no return, parameter, field, or other expected Result type is known.",
		"Add a Result<T, E> type to the enclosing declaration or pass the expression to a typed Result context."),
	defineDiagnostic(diagnosticCodePropagateValue, DiagnosticPhaseTypeCheck,
		"Propagation requires a Result",
		"The postfix ? operator unwraps a Result success value and returns its error variant early.",
		"The operand of ? is not a Result value.",
		"Use ? only on Result<T, E>, or remove it and handle the value directly."),
	defineDiagnostic(diagnosticCodePropagateReturn, DiagnosticPhaseTypeCheck,
		"Enclosing function cannot propagate a Result",
		"A function using ? must itself return a Result so an error can leave early.",
		"The enclosing function's declared return type is not Result<T, E>.",
		"Change the function return type to Result<T, E>, or handle the operand without ?."),
	defineDiagnostic(diagnosticCodePropagateError, DiagnosticPhaseTypeCheck,
		"Propagated error type does not match",
		"The error type propagated by ? must equal the enclosing function's Result error type.",
		"The operand and enclosing function use different Result failure types.",
		"Convert the error to the enclosing failure type, handle it locally, or align the two Result types."),
	defineDiagnostic(diagnosticCodeMatchValue, DiagnosticPhaseTypeCheck,
		"Match requires a Result or union",
		"Match arms destructure only Result<T, E> values and values of a declared union; an optional is neither, because null is not an implicit variant.",
		"A match expression receives a value whose type is neither Result nor a union.",
		"Match a Result or union value, or replace the construct with control flow for the actual type.",
		"Compare an optional with null first and match the value the branch proved present."),
	defineDiagnostic(diagnosticCodeMatchExhaustiveness, DiagnosticPhaseTypeCheck,
		"Match is not exhaustive",
		"A match must handle both Ok and Err for a Result, and every declared variant of a union, unless a catch-all arm handles the remainder.",
		"The match omits a reachable Result or union variant.",
		"Add the missing Ok, Err, or union variant arm, or add a catch-all _ arm."),
	defineDiagnostic(diagnosticCodeMatchArm, DiagnosticPhaseTypeCheck,
		"Match arm is invalid",
		"Each variant can be handled once, and no arm may follow a catch-all or complete set of variants.",
		"A match arm duplicates an earlier arm or can never be reached.",
		"Remove or reorder the duplicate or unreachable arm."),
	defineDiagnostic(diagnosticCodeMatchArmType, DiagnosticPhaseTypeCheck,
		"Match arms have incompatible types",
		"Every reachable arm of a match must produce one common result type.",
		"Two reachable match arms produce types that cannot be joined.",
		"Change the arms to produce the same type."),
	defineDiagnostic(diagnosticCodeResultConstructorArity, DiagnosticPhaseParse,
		"Result constructor has the wrong arity",
		"Ok and Err each contain exactly one payload expression.",
		"A Result constructor is written with zero or multiple arguments.",
		"Supply exactly one payload to Ok or Err."),
	defineDiagnostic(diagnosticCodeMatchPattern, DiagnosticPhaseParse,
		"Unsupported match pattern",
		"Matches support Ok(...), Err(...), qualified Union.Variant patterns, and _.",
		"A match arm starts with another pattern shape, or a variant pattern has empty parentheses.",
		"Replace the pattern with Ok(binding), Err(binding), Union.Variant(bindings), or _."),
	defineDiagnostic(diagnosticCodeGenericType, DiagnosticPhaseTypeCheck,
		"Invalid generic type",
		"A generic type application must name a known generic, use its declared arity, and satisfy its type-argument restrictions.",
		"A type is malformed, unknown, has the wrong number of arguments, or uses an invalid Map key type.",
		"Use a known generic with exactly its declared type arguments and valid constrained types."),
	defineDiagnostic(diagnosticCodeOptionalReceiver, DiagnosticPhaseTypeCheck,
		"Optional value may be null",
		"A member cannot be accessed through an optional value until the value has been proved present.",
		"A field or method is accessed through T?.",
		"Compare the value with null and use it inside the present branch.",
		"Propagate the absence or provide an explicit fallback.").
		withExamples("User.Name", "if (User != null) {\n  User.Name\n}").
		withRelated(diagnosticCodeNullTarget, diagnosticCodeOptionalValue, diagnosticCodeOptionalComparison, diagnosticCodeOptionalCondition),
	defineDiagnostic(diagnosticCodeNullTarget, DiagnosticPhaseTypeCheck,
		"Null requires an optional type",
		"Null can be stored only where the expected type is optional.",
		"A null literal is returned, assigned, or passed to a required T location.",
		"Make the receiving type optional, or provide a non-null value of the required type.").
		withRelated(diagnosticCodeOptionalValue),
	defineDiagnostic(diagnosticCodeOptionalValue, DiagnosticPhaseTypeCheck,
		"Optional value requires narrowing",
		"A T? value is not assignable where a present T value is required until control flow proves it non-null.",
		"An optional value is returned, assigned, or passed to a required type.",
		"Compare the value with null and use it in the branch where it is present.",
		"Change the destination to T? when absence is part of its contract.").
		withRelated(diagnosticCodeOptionalReceiver, diagnosticCodeNullTarget),
	defineDiagnostic(diagnosticCodeRedundantOptional, DiagnosticPhaseTypeCheck,
		"Optional marker is redundant",
		"A Slick type is optional at most once; T?? has the same intended absence shape as T? but is not canonical.",
		"A declared type contains more than one consecutive optional suffix.",
		"Remove every extra ? so the type has a single optional marker."),
	defineDiagnostic(diagnosticCodeOptionalComparison, DiagnosticPhaseTypeCheck,
		"Optional comparison is invalid",
		"Equality involving optional values is defined only when the compared types can represent the same present values or null.",
		"An optional or null value is compared with an unrelated type.",
		"Compare compatible optional types, or compare the optional value directly with null."),
	defineDiagnostic(diagnosticCodeOptionalCondition, DiagnosticPhaseTypeCheck,
		"Optional boolean is not a condition",
		"A condition must be a present bool; bool? may be null and has no implicit truth value.",
		"An optional boolean is used directly as an if condition.",
		"Compare the value with true or null, or provide an explicit fallback boolean."),
	defineDiagnostic(diagnosticCodeRequiredField, DiagnosticPhaseTypeCheck,
		"Required field is missing",
		"Object construction must initialize every non-optional field declared by the class.",
		"An object literal omits a required field.",
		"Provide a value of the declared type for every missing required field."),
	defineDiagnostic(diagnosticCodeTypeArguments, DiagnosticPhaseTypeCheck,
		"Type arguments do not match",
		"A callable either accepts no type arguments or requires exactly its declared number of type arguments.",
		"A call supplies type arguments to a nongeneric callable or uses the wrong generic arity.",
		"Remove unsupported type arguments or supply exactly the declared number."),
	defineDiagnostic(diagnosticCodeJSONType, DiagnosticPhaseTypeCheck,
		"Type is not supported by JSON",
		"JSON encoding and decoding require a structurally supported Slick data type.",
		"A JSON operation is instantiated with an interface, Result, inaccessible field, or another unsupported shape.",
		"Use a supported data class with accessible JSON-compatible fields.",
		"Convert the value to a supported representation before encoding or after decoding."),
	defineDiagnostic(diagnosticCodeMapTypeUnknown, DiagnosticPhaseTypeCheck,
		"Map type cannot be inferred",
		"An empty map literal needs an expected Map<K, V> type because it contains no entries from which to infer K and V.",
		"An empty map literal appears without a typed return, assignment, argument, or field context.",
		"Add an expected Map<K, V> type around the empty literal."),
	defineDiagnostic(diagnosticCodeMapKeyType, DiagnosticPhaseTypeCheck,
		"Map key type is invalid",
		"Map keys must be string, int, or bool so equality and native representation stay deterministic.",
		"A map literal contains a key with another type.",
		"Convert or replace every key with a string, int, or bool value."),
	defineDiagnostic(diagnosticCodeDuplicateMapKey, DiagnosticPhaseTypeCheck,
		"Map key is duplicated",
		"A map literal cannot initialize the same statically known key more than once.",
		"Two literal entries use the same constant key.",
		"Remove one entry or give each entry a unique key."),
	defineDiagnostic(diagnosticCodeCloseMethod, DiagnosticPhaseResourceCheck,
		"Resource has no accessible Close method",
		"A value managed by using must expose an accessible Close method.",
		"The using binding's type has no visible Close method.",
		"Add an accessible Close method, or do not manage that value with using."),
	defineDiagnostic(diagnosticCodeCloseParameters, DiagnosticPhaseResourceCheck,
		"Close method takes arguments",
		"A resource Close method invoked by using must take no arguments.",
		"The managed type declares Close with one or more parameters.",
		"Change Close to take no arguments."),
	defineDiagnostic(diagnosticCodeCloseResult, DiagnosticPhaseResourceCheck,
		"Close method returns a value",
		"A resource Close method invoked by using must return null.",
		"The managed type's Close method declares another return type.",
		"Change Close to return null and handle cleanup failures inside its declared error contract."),
	defineDiagnostic(diagnosticCodeManualClose, DiagnosticPhaseResourceCheck,
		"Using resource is closed manually",
		"A using scope owns cleanup and calls Close exactly once when the scope exits.",
		"Code calls Close directly on a binding currently managed by using.",
		"Remove the direct Close call and let the using scope close the resource."),
	defineDiagnostic(diagnosticCodeUsingEscape, DiagnosticPhaseResourceCheck,
		"Using resource escapes its scope",
		"A value owned by a using scope cannot be returned, assigned outside that scope, or otherwise outlive automatic cleanup.",
		"A using binding flows into storage or control flow that survives the using block.",
		"Consume the resource entirely inside the using scope.",
		"Create or transfer an unmanaged value when ownership must outlive the scope."),
	defineDiagnostic(diagnosticCodeUsingAssignment, DiagnosticPhaseResourceCheck,
		"Using binding is immutable",
		"A using binding keeps one resource identity so the compiler can close exactly the value it acquired.",
		"Code assigns a new value to an active using binding.",
		"Keep the binding unchanged and assign the replacement to a separate local."),
	defineDiagnostic(diagnosticCodeOrphanDocumentation, DiagnosticPhaseDocumentation,
		"Documentation comment is not attached",
		"A documentation comment must immediately precede a declaration that can carry documentation.",
		"A /// block is separated from a describable declaration or precedes unsupported syntax.",
		"Move the comment directly above a describable declaration, or change it to an ordinary // comment."),
	defineDiagnostic(diagnosticCodeConflictingDocumentation, DiagnosticPhaseDocumentation,
		"Declaration has competing documentation",
		"One canonical declaration has one documentation string.",
		"Merged declarations or method forms provide more than one docstring for the same canonical symbol.",
		"Keep documentation on one canonical declaration and remove the competing block."),
	defineDiagnostic(diagnosticCodeResourceRequiresUsing, DiagnosticPhaseResourceCheck,
		"Resource requires a using scope",
		"Standard I/O resources must be owned by using so cleanup occurs on every exit path.",
		"A standard reader or writer is used outside automatic using ownership.",
		"Acquire and consume the resource inside a using scope."),
	defineDiagnostic(diagnosticCodeAsyncInitializer, DiagnosticPhaseTypeCheck,
		"Invalid async initializer",
		"An async let starts exactly one existing function or method call.",
		"The initializer is not a resolved function or method call.",
		"Call one function or method directly after the equals sign."),
	defineDiagnostic(diagnosticCodePendingUse, DiagnosticPhaseTypeCheck,
		"Pending binding used as a value",
		"A pending binding is compiler-owned task state and has no public value representation.",
		"Code reads, stores, passes, returns, or otherwise uses a pending binding without await.",
		"Use the binding only as the direct operand of await."),
	defineDiagnostic(diagnosticCodePendingAssignment, DiagnosticPhaseTypeCheck,
		"Pending binding is immutable",
		"An async let binding identifies one lexical child until await consumes it.",
		"Code assigns to a pending binding.",
		"Remove the assignment and await the original binding."),
	defineDiagnostic(diagnosticCodeAwaitUnknown, DiagnosticPhaseNameResolution,
		"Unknown pending binding",
		"Await can consume only an async let binding visible in the current lexical scope.",
		"The await operand does not name a visible pending binding.",
		"Fix the name or declare the async let in an enclosing block."),
	defineDiagnostic(diagnosticCodeAwaitOrdinary, DiagnosticPhaseTypeCheck,
		"Ordinary value awaited",
		"Only async let bindings are pending and awaitable.",
		"The await operand names an ordinary local or parameter.",
		"Remove await or await an async let binding."),
	defineDiagnostic(diagnosticCodeAwaitTwice, DiagnosticPhaseTypeCheck,
		"Pending binding awaited twice",
		"Await consumes its pending binding exactly once.",
		"A control-flow path reaches a second await of the same binding.",
		"Keep one await and reuse its ordinary result value."),
	defineDiagnostic(diagnosticCodePendingMissingAwait, DiagnosticPhaseTypeCheck,
		"Pending binding is not awaited",
		"Every normal path out of an owning block must consume each pending binding exactly once.",
		"A normal block exit leaves an async let binding pending.",
		"Await the binding on every normal path or after the branch."),
	defineDiagnostic(diagnosticCodePendingPath, DiagnosticPhaseTypeCheck,
		"Pending binding consumption differs by branch",
		"A pending binding must be consumed exactly once on every normal control-flow path.",
		"Some normal branches await a binding while others do not.",
		"Await it in every normal branch or once after the branch."),
	defineDiagnostic(diagnosticCodeAwaitLoop, DiagnosticPhaseTypeCheck,
		"Pending binding awaited from a repeating loop",
		"An outer pending binding cannot be consumed by more than one loop iteration.",
		"Await occurs in a loop deeper than the binding's owning block.",
		"Await before or after the loop, or declare and await the async let inside one iteration."),
	defineDiagnostic(diagnosticCodeAsyncUnsafeValue, DiagnosticPhaseTypeCheck,
		"Task-unsafe value crosses into a child",
		"Child receivers and arguments must be immutable structural values without native resources or resource-hiding interfaces.",
		"An async call captures a native resource, structural resource interface, or unsupported contained type.",
		"Pass immutable data instead and acquire resources inside the child."),
	defineDiagnostic(diagnosticCodeUnionVariant, DiagnosticPhaseTypeCheck,
		"Invalid union variant",
		"A union declares a closed set of variants, so every construction and every pattern must name one of them and account for exactly its declared payload fields.",
		"A construction or match pattern names an unknown variant, a variant of another union, or binds the wrong number of payload values.",
		"Name a variant declared by the union being constructed or matched.",
		"Bind exactly one name per declared payload field, using _ to ignore one.",
		"Write a fieldless variant without parentheses and a variant with fields with them."),
	defineDiagnostic(diagnosticCodeConstantType, DiagnosticPhaseTypeCheck,
		"Constant type is not supported",
		"A constant holds one compile-time value, so its declared type must be a language primitive or a union whose fieldless variants name that value.",
		"A constant is declared with an optional, collection, class, interface, or generic type.",
		"Declare the constant bool, int, float, string, null, or a union.",
		"Compute the value in a function when it needs a type constants do not carry."),
	defineDiagnostic(diagnosticCodeConstantExpression, DiagnosticPhaseTypeCheck,
		"Constant expression is not allowed",
		"A constant initializer is limited to expressions the compiler evaluates with no runtime state: literals, other constants, fieldless union variants, grouping, unary - and !, and the non-failing operators.",
		"An initializer calls a function, constructs a value, interpolates a template, reads a field, or uses another runtime construct.",
		"Replace the initializer with a constant expression.",
		"Move the computation into a function when it needs runtime state."),
	defineDiagnostic(diagnosticCodeConstantAssignment, DiagnosticPhaseTypeCheck,
		"Constant is immutable",
		"A constant names one compile-time value and has no storage to write.",
		"An assignment statement targets a constant.",
		"Bind a local with let when the value must change.",
		"Rename the local when it was meant to be separate from the constant."),
	defineDiagnostic(diagnosticCodeConstantCycle, DiagnosticPhaseTypeCheck,
		"Constant declarations form a cycle",
		"Constants are evaluated once at compile time, so their references must form an acyclic graph; forward references are valid only while they stay acyclic.",
		"A constant reaches itself through its own initializer, directly or through other constants.",
		"Break the cycle by giving one constant on the reported chain a value that does not depend on the others."),
	defineDiagnostic(diagnosticCodeTypeParameter, DiagnosticPhaseParse,
		"Type parameter declaration is invalid",
		"A declaration's type parameters are distinct names that do not shadow a compiler-owned type.",
		"A type parameter list repeats a name or reuses a built-in type name.",
		"Rename the parameter to a distinct name that no built-in type already uses."),
	defineDiagnostic(diagnosticCodeUnboundTypeParameter, DiagnosticPhaseNameResolution,
		"Type name is not bound",
		"Every type named inside a generic declaration must be a known type or one of the parameters that declaration binds.",
		"A declared type names something that is neither visible nor a type parameter in scope.",
		"Declare the missing type parameter on the enclosing declaration.",
		"Correct the type name or import the namespace that declares it."),
	defineDiagnostic(diagnosticCodeGenericExpansion, DiagnosticPhaseTypeCheck,
		"Generic instantiation expands without limit",
		"A recursive generic may contain itself only at the same type arguments, so the set of instantiations stays finite.",
		"A declaration instantiates itself with a type argument built from its own parameter.",
		"Hold the recursive value at the same type arguments, such as Node<T> inside Node<T>."),
	defineDiagnostic(diagnosticCodeNotCallable, DiagnosticPhaseTypeCheck,
		"Value is not callable",
		"Call syntax applies to a named function, a method of the receiver's type, or any expression whose static type is a callable.",
		"A call target names or produces a value that is not a callable type.",
		"Call a function, a method, or a value declared with a callable type such as (int, int) -> int.",
		"Remove the call parentheses when the value itself was intended.").
		withRelated(diagnosticCodeMethodValue, diagnosticCodeUnknownCallable),
	defineDiagnostic(diagnosticCodeMethodValue, DiagnosticPhaseTypeCheck,
		"Method is not a value",
		"Methods are invoked through their receiver; Slick has no bound or unbound method value.",
		"A method name is read without being called.",
		"Call the method with its arguments.",
		"Wrap the call in a lambda when a callable value is required.").
		withExamples("Record.Transform", "(Value: int) -> int {\n  Record.Transform(Value)\n}").
		withRelated(diagnosticCodeNotCallable),
	defineDiagnostic(diagnosticCodeLambdaCapture, DiagnosticPhaseTypeCheck,
		"Invalid lambda capture",
		"A lambda copies the surrounding bindings it reads by value when it is created, so a capture is read-only, cannot be a pending or actively managed binding, and cannot be the binding the lambda itself initializes.",
		"A lambda assigns a captured binding, captures a pending async or active using binding, or refers to the let binding whose initializer it is.",
		"Bind a separate local instead of assigning the captured value.",
		"Await the pending binding or consume the resource before creating the lambda.",
		"Use a named function when the callable has to refer to itself.").
		withRelated(diagnosticCodePendingUse, diagnosticCodeUsingEscape),
	defineDiagnostic(diagnosticCodeAnnotation, DiagnosticPhaseNameResolution,
		"Annotation cannot be resolved",
		"Every annotation application and alias target must resolve through the ordinary namespace, visibility, and exact-alias rules.",
		"An annotation name is unknown, inaccessible, or conflicts with another declaration.",
		"Declare or import the annotation and use its visible canonical name."),
	defineDiagnostic(diagnosticCodeAnnotationTarget, DiagnosticPhaseTypeCheck,
		"Annotation target is invalid",
		"A compiler-owned terminal annotation declares its allowed targets and repeatability; source aliases inherit that contract.",
		"An annotation is applied to an unsupported target or a non-repeatable terminal is applied more than once.",
		"Move the annotation to an allowed class, method, or parameter target.",
		"Remove the duplicate application."),
	defineDiagnostic(diagnosticCodeAnnotationArgument, DiagnosticPhaseTypeCheck,
		"Annotation argument is invalid",
		"Annotation arguments are checked compile-time values: literals, constants, fieldless union variants, alias parameters, or non-generic named functions.",
		"An argument has the wrong arity or type, or uses an expression form annotations do not execute.",
		"Pass a value assignable to the declared annotation parameter.",
		"Replace lambdas, method values, and generic functions with a compatible named function."),
	defineDiagnostic(diagnosticCodeAnnotationCycle, DiagnosticPhaseNameResolution,
		"Annotation aliases form a cycle",
		"Alias expansion must reach one compiler-owned terminal through an acyclic deterministic chain.",
		"An annotation alias reaches itself directly or indirectly.",
		"Break the reported chain by targeting a terminal or another acyclic alias."),
	defineDiagnostic(diagnosticCodeUnwrapValue, DiagnosticPhaseTypeCheck,
		"Unwrap requires a Result",
		"unwrap extracts the success payload from a Result and throws the failure. It is not optional-null coalescing.",
		"The argument to unwrap is not a Result value.",
		"Pass a Result<T, E> whose E implements Error.",
		"Match the value, or compare an optional with null."),
	defineDiagnostic(diagnosticCodeUnreadBinding, DiagnosticPhaseLint,
		"Local binding is never read",
		"A let binding names a value so a later expression can read it. A binding with no read states an intent the program never uses, which is dead source the compiler accepts.",
		"A let binding's lexical identity has zero reads, counting reads through fields, methods, interpolation, nested branches, and lambda captures.",
		"Remove the statement when the initializer is pure.",
		"Keep the expression and drop the binding when the initializer has effects, such as Work()? instead of let Result = Work()?.",
		"Bind _ in a destructuring pattern to state an explicit discard.").
		withExamples("let Unused = Value + 1\nValue", "Value").
		asWarning(),
	defineDiagnostic(diagnosticCodeDiscardedExpression, DiagnosticPhaseLint,
		"Pure expression result is discarded",
		"Only a block's final expression is its value, so an earlier expression statement is evaluated for its effects. An expression proved free of effects has none, and the statement does nothing.",
		"A non-final expression statement's tree is built only from literals, reads, lambdas, construction, and built-in operators over pure children.",
		"Delete the statement.",
		"Bind the value with let and read it.",
		"Make the value the block's final expression.").
		withExamples("\"discarded\"\n\"ok\"", "\"ok\"").
		withRelated(diagnosticCodeUnreadBinding).
		asWarning(),
	defineDiagnostic(diagnosticCodeUnreachableStatement, DiagnosticPhaseLint,
		"Statement is unreachable",
		"return, throw, break, and continue leave their block, so a statement written after one in the same block never runs.",
		"A statement directly follows return, throw, break, or continue inside one block.",
		"Delete the statements after the terminator.",
		"Move the terminator after the work it was meant to follow.").
		withExamples("return 1\n2", "1").
		asWarning(),
	defineDiagnostic(diagnosticCodeCyclomaticComplexity, DiagnosticPhaseQuality,
		"Cyclomatic complexity exceeds its limit",
		"Cyclomatic complexity counts the independent decisions one callable makes. The score starts at 1 and adds 1 for each if, each for, each short-circuit && or ||, and each postfix ?, plus N-1 for a match with N arms and N for a catch with N arms. A lambda is scored separately from the callable that contains it.",
		"An authored function, method, or lambda scores above 10.",
		"Split independent decisions into separate callables that each own one alternative.",
		"Flatten control flow with else if or an early return instead of nesting.",
		"Replace a chain of short-circuit operators with one named predicate.").
		withExamples("if (Ready && Allowed && !Expired && Fresh) {\n    1\n} else {\n    0\n}", "if (Accepted(Ready, Allowed, Expired, Fresh)) {\n    1\n} else {\n    0\n}").
		withRelated(diagnosticCodeCognitiveComplexity).
		asWarning(),
	defineDiagnostic(diagnosticCodeCognitiveComplexity, DiagnosticPhaseQuality,
		"Cognitive complexity exceeds its limit",
		"Cognitive complexity counts nested mental context rather than length. if, for, match, and each catch arm cost 1 plus the current control-flow nesting; a source-level else if costs 1 without another nesting penalty; each maximal run of one short-circuit operator costs 1; postfix ? costs 1 for its implicit early exit. using is transparent, and a lambda starts its own score at nesting zero.",
		"An authored function, method, or lambda scores above 15.",
		"Extract a nested branch into a named callable, where it reads at nesting zero.",
		"Replace a tower of nested if/else blocks with an else if chain.",
		"Return early so the remaining work is not nested.").
		withExamples("if (A) {\n    1\n} else {\n    if (B) {\n        2\n    } else {\n        3\n    }\n}", "if (A) {\n    1\n} else if (B) {\n    2\n} else {\n    3\n}").
		withRelated(diagnosticCodeCyclomaticComplexity).
		asWarning(),
}

var diagnosticRegistry = mustBuildDiagnosticRegistry(diagnosticDefinitions)

func IsDiagnosticCode(value string) bool {
	if len(value) != len("SLK000") || !strings.HasPrefix(value, "SLK") {
		return false
	}
	for index := 3; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func DescribeDiagnostic(code string) (DiagnosticDescription, error) {
	definition, ok := diagnosticRegistry[diagnosticCode(code)]
	if !ok {
		return DiagnosticDescription{}, fmt.Errorf("%w %q", ErrUnknownDiagnostic, code)
	}
	description := DiagnosticDescription{
		Code:        string(definition.Code),
		Severity:    definition.Severity,
		Phase:       definition.Phase,
		Title:       definition.Title,
		Explanation: definition.Explanation,
		Trigger:     definition.Trigger,
		Fixes:       append([]string(nil), definition.Fixes...),
		Related:     make([]string, len(definition.Related)),
	}
	if definition.InvalidExample != "" {
		description.InvalidExample = stringPointer(definition.InvalidExample)
	}
	if definition.ValidExample != "" {
		description.ValidExample = stringPointer(definition.ValidExample)
	}
	for index, related := range definition.Related {
		description.Related[index] = string(related)
	}
	return description, nil
}

func stringPointer(value string) *string {
	return &value
}

func mustBuildDiagnosticRegistry(definitions []diagnosticDefinition) map[diagnosticCode]diagnosticDefinition {
	registry, err := buildDiagnosticRegistry(definitions)
	if err != nil {
		panic(err)
	}
	return registry
}

func buildDiagnosticRegistry(definitions []diagnosticDefinition) (map[diagnosticCode]diagnosticDefinition, error) {
	registry := make(map[diagnosticCode]diagnosticDefinition, len(definitions))
	var previous diagnosticCode
	for index, definition := range definitions {
		if !IsDiagnosticCode(string(definition.Code)) {
			return nil, fmt.Errorf("invalid diagnostic code %q", definition.Code)
		}
		if _, duplicate := registry[definition.Code]; duplicate {
			return nil, fmt.Errorf("duplicate diagnostic code %s", definition.Code)
		}
		if index > 0 && definition.Code < previous {
			return nil, fmt.Errorf("diagnostic definitions are not ordered by code: %s follows %s", definition.Code, previous)
		}
		previous = definition.Code
		if !validDiagnosticSeverity(definition.Severity) {
			return nil, fmt.Errorf("diagnostic %s has invalid severity %q", definition.Code, definition.Severity)
		}
		if isWarningPhase(definition.Phase) != (definition.Severity == DiagnosticSeverityWarning) {
			return nil, fmt.Errorf("diagnostic %s pairs phase %q with severity %q", definition.Code, definition.Phase, definition.Severity)
		}
		if !validDiagnosticPhase(definition.Phase) {
			return nil, fmt.Errorf("diagnostic %s has invalid phase %q", definition.Code, definition.Phase)
		}
		if definition.Title == "" || definition.Explanation == "" || definition.Trigger == "" {
			return nil, fmt.Errorf("diagnostic %s has incomplete description", definition.Code)
		}
		if len(definition.Fixes) == 0 {
			return nil, fmt.Errorf("diagnostic %s has no repair strategy", definition.Code)
		}
		texts := append([]string{definition.Title, definition.Explanation, definition.Trigger, definition.InvalidExample, definition.ValidExample}, definition.Fixes...)
		for _, text := range texts {
			if strings.ContainsRune(text, '\x1b') {
				return nil, fmt.Errorf("diagnostic %s contains an ANSI escape", definition.Code)
			}
		}
		registry[definition.Code] = definition
	}
	for _, definition := range definitions {
		seen := make(map[diagnosticCode]struct{}, len(definition.Related))
		for _, related := range definition.Related {
			if related == definition.Code {
				return nil, fmt.Errorf("diagnostic %s relates to itself", definition.Code)
			}
			if _, duplicate := seen[related]; duplicate {
				return nil, fmt.Errorf("diagnostic %s repeats related code %s", definition.Code, related)
			}
			seen[related] = struct{}{}
			if _, exists := registry[related]; !exists {
				return nil, fmt.Errorf("diagnostic %s relates to unknown code %s", definition.Code, related)
			}
		}
	}
	return registry, nil
}

func validDiagnosticPhase(phase DiagnosticPhase) bool {
	switch phase {
	case DiagnosticPhaseParse,
		DiagnosticPhaseNameResolution,
		DiagnosticPhaseTypeCheck,
		DiagnosticPhaseEffectCheck,
		DiagnosticPhaseMethodCheck,
		DiagnosticPhaseVisibilityCheck,
		DiagnosticPhaseResourceCheck,
		DiagnosticPhaseDocumentation,
		DiagnosticPhaseLint,
		DiagnosticPhaseQuality:
		return true
	default:
		return false
	}
}

func validDiagnosticSeverity(severity DiagnosticSeverity) bool {
	switch severity {
	case DiagnosticSeverityError, DiagnosticSeverityWarning:
		return true
	default:
		return false
	}
}

// isWarningPhase names the phases that examine an already valid program. Their
// findings are warnings by construction: compilation has already succeeded.
func isWarningPhase(phase DiagnosticPhase) bool {
	return phase == DiagnosticPhaseLint || phase == DiagnosticPhaseQuality
}

func newDiagnostic(pos position, code diagnosticCode, format string, args ...any) Diagnostic {
	definition, registered := diagnosticRegistry[code]
	if !registered {
		panic(fmt.Sprintf("unregistered diagnostic code %q", code))
	}
	return Diagnostic{
		File:     pos.file,
		Line:     pos.line,
		Column:   pos.column,
		Code:     string(code),
		Severity: definition.Severity,
		Message:  fmt.Sprintf(format, args...),
	}
}

// sortDiagnostics puts diagnostics in the one total order every gate reports:
// file, line, column, then code.
func sortDiagnostics(diagnostics []Diagnostic) {
	sort.SliceStable(diagnostics, func(first, second int) bool {
		left, right := diagnostics[first], diagnostics[second]
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Column != right.Column {
			return left.Column < right.Column
		}
		return left.Code < right.Code
	})
}
