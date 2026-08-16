package compiler

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type llvmGen struct {
	program      *program
	out          strings.Builder
	decl         strings.Builder
	strs         map[string]string
	strN         int
	next         int
	typeNames    []string
	typeID       map[string]int
	fieldIdx     map[string]map[string]int
	unionNames   []string
	unionID      map[string]int
	ifaceMethods map[string][]string
	vtables      map[string]string
	jsonTasks    map[string]string
	fn           *llvmFn
}

type llvmFn struct {
	body  strings.Builder
	next  int
	block int
	cur   string
}

type llvmBind struct {
	name     string
	typ      string
	storage  string
	declared string
}

type llvmScope struct {
	function  *functionDecl
	locals    map[string]llvmBind
	pending   map[string]string
	taskScope string
}

func newLLVMBind(name, typ string) llvmBind {
	return llvmBind{name: name, typ: typ, storage: name, declared: typ}
}

func (g *llvmGen) setLocal(scope *llvmScope, name, typ, value string) {
	if bind, ok := scope.locals[name]; ok && bind.storage != "" {
		g.emit("  store %%slick.val %s, ptr %s, align 8", value, bind.storage)
		scope.locals[name] = llvmBind{name: bind.storage, typ: typ, storage: bind.storage, declared: typ}
		return
	}
	slot := g.reg()
	g.emit("  %s = alloca %%slick.val, align 8", slot)
	g.emit("  store %%slick.val %s, ptr %s, align 8", value, slot)
	scope.locals[name] = llvmBind{name: slot, typ: typ, storage: slot, declared: typ}
}

func (g *llvmGen) loadBind(bind llvmBind) string {
	v := g.reg()
	g.emit("  %s = load %%slick.val, ptr %s, align 8", v, bind.storage)
	return v
}

func (s *llvmScope) clone() *llvmScope {
	locals := make(map[string]llvmBind, len(s.locals))
	for k, v := range s.locals {
		locals[k] = v
	}
	pending := make(map[string]string, len(s.pending))
	for k, v := range s.pending {
		pending[k] = v
	}
	return &llvmScope{function: s.function, locals: locals, pending: pending, taskScope: s.taskScope}
}

func (p *program) generateLLVM() (string, error) {
	main := p.functions["root.main"]
	if main == nil {
		return "", fmt.Errorf("root.main is not defined")
	}
	if p.resolveType(main.namespace, main.aliases, main.result) == stdProcessStatusName {
		p.usesStdProcess = true
	}
	g := &llvmGen{
		program:      p,
		strs:         map[string]string{},
		typeID:       map[string]int{},
		fieldIdx:     map[string]map[string]int{},
		unionID:      map[string]int{},
		ifaceMethods: map[string][]string{},
		vtables:      map[string]string{},
		jsonTasks:    map[string]string{},
	}
	g.collectTypes()
	g.emitHeader()
	g.emitRuntimeDecls()
	if err := g.emitUserFunctions(); err != nil {
		return "", err
	}
	if err := g.emitMain(); err != nil {
		return "", err
	}
	return g.decl.String() + g.out.String(), nil
}

func (g *llvmGen) skipName(name string) bool {
	if strings.HasPrefix(name, "std.io.") && !g.program.usesStdIO {
		return true
	}
	if strings.HasPrefix(name, "std.http.server.") && !g.program.usesStdHTTPServer {
		return true
	}
	if strings.HasPrefix(name, "std.http.") && !g.program.usesStdHTTP && !strings.HasPrefix(name, "std.http.server.") {
		return true
	}
	if strings.HasPrefix(name, "std.fs.") && !g.program.usesStdFSDirectory &&
		(strings.Contains(name, "Temporary") || strings.Contains(name, "ReadDirectory") || strings.Contains(name, "Entry")) {
		return true
	}
	if strings.HasPrefix(name, "std.process.") && !g.program.usesStdProcess {
		return true
	}
	if strings.HasPrefix(name, "std.sqlite.") && !g.program.usesStdSQLite {
		return true
	}
	return false
}

func (g *llvmGen) collectTypes() {
	for _, name := range sortedKeys(g.program.classes) {
		if g.skipName(name) {
			continue
		}
		g.typeID[name] = len(g.typeNames)
		g.typeNames = append(g.typeNames, name)
		idx := map[string]int{}
		for i, field := range sortedKeys(g.program.classes[name].fields) {
			idx[field] = i
		}
		g.fieldIdx[name] = idx
	}
	for _, name := range sortedKeys(g.program.interfaces) {
		if g.skipName(name) {
			continue
		}
		g.typeID[name] = len(g.typeNames)
		g.typeNames = append(g.typeNames, name)
		g.ifaceMethods[name] = sortedKeys(g.program.interfaces[name].methods)
	}
	for _, name := range sortedKeys(g.program.unions) {
		if g.skipName(name) {
			continue
		}
		g.unionID[name] = len(g.unionNames)
		g.unionNames = append(g.unionNames, name)
	}
}

func (g *llvmGen) emitHeader() {
	g.decl.WriteString("target datalayout = \"e-m:e-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128\"\n")
	g.decl.WriteString("target triple = \"x86_64-pc-linux-gnu\"\n")
	g.decl.WriteString("%slick.val = type { i64, i64 }\n")
	g.decl.WriteString("%slick.out = type { i64, %slick.val }\n\n")
	fmt.Fprintf(&g.decl, "@slick_abi_version_%d = external constant i32\n\n", NativeABIVersion)
}

func (g *llvmGen) emitRuntimeDecls() {
	decls := []string{
		"declare %slick.val @slick_rt_bool(i32)",
		"declare %slick.val @slick_rt_int(i64)",
		"declare %slick.val @slick_rt_float(double)",
		"declare %slick.val @slick_rt_string(ptr, i64)",
		"declare %slick.val @slick_rt_bytes(ptr, i64)",
		"declare %slick.val @slick_rt_array(i32, i64, ptr)",
		"declare %slick.val @slick_rt_optional(i32, %slick.val)",
		"declare %slick.val @slick_rt_none()",
		"declare %slick.val @slick_rt_some(%slick.val)",
		"declare %slick.val @slick_rt_result(i32, %slick.val)",
		"declare %slick.val @slick_rt_class(i32, i32, ptr)",
		"declare %slick.val @slick_rt_union(i32, i32, i32, ptr)",
		"declare %slick.val @slick_rt_callable(ptr, i32, ptr, i32)",
		"declare %slick.val @slick_rt_iface(i32, %slick.val, i32, ptr)",
		"declare %slick.val @slick_rt_iter_range(i64, i64)",
		"declare %slick.val @slick_rt_iter_of(%slick.val)",
		"declare %slick.val @slick_rt_iter_enum(%slick.val)",
		"declare %slick.val @slick_rt_iter_zip(i64, ptr)",
		"declare i64 @slick_rt_iter_len(%slick.val)",
		"declare %slick.val @slick_rt_iter_item(%slick.val, i64)",
		"declare %slick.val @slick_rt_iter_at(%slick.val, i64, i32)",
		"declare %slick.val @slick_rt_field(%slick.val, i32)",
		"declare void @slick_rt_set_field(%slick.val, i32, %slick.val)",
		"declare %slick.val @slick_rt_union_field(%slick.val, i32)",
		"declare i32 @slick_rt_union_tag(%slick.val)",
		"declare i32 @slick_rt_class_type(%slick.val)",
		"declare i32 @slick_rt_is_error(%slick.val)",
		"declare i32 @slick_rt_optional_present(%slick.val)",
		"declare %slick.val @slick_rt_optional_value(%slick.val)",
		"declare i32 @slick_rt_result_ok(%slick.val)",
		"declare %slick.val @slick_rt_result_payload(%slick.val)",
		"declare %slick.val @slick_rt_promote(%slick.val, i32)",
		"declare %slick.val @slick_rt_map(i64, ptr, ptr)",
		"declare %slick.val @slick_rt_empty_map()",
		"declare %slick.val @slick_rt_map_get(%slick.val, %slick.val)",
		"declare i32 @slick_rt_map_contains(%slick.val, %slick.val)",
		"declare %slick.val @slick_rt_map_with(%slick.val, %slick.val, %slick.val)",
		"declare %slick.val @slick_rt_map_without(%slick.val, %slick.val)",
		"declare i64 @slick_rt_map_len(%slick.val)",
		"declare %slick.val @slick_rt_buffer_new()",
		"declare void @slick_rt_buffer_push(%slick.val, %slick.val)",
		"declare %slick.val @slick_rt_buffer_get(%slick.val, i64)",
		"declare %slick.val @slick_rt_buffer_set(%slick.val, i64, %slick.val, i32)",
		"declare i64 @slick_rt_buffer_len(%slick.val)",
		"declare %slick.val @slick_rt_buffer_freeze(%slick.val)",
		"declare i64 @slick_rt_array_len(%slick.val)",
		"declare %slick.val @slick_rt_array_get(%slick.val, i64)",
		"declare %slick.val @slick_rt_array_index(%slick.val, i64)",
		"declare %slick.val @slick_rt_array_slice(%slick.val, i64, i64, i32)",
		"declare i32 @slick_rt_equal(%slick.val, %slick.val)",
		"declare %slick.val @slick_rt_format(%slick.val)",
		"declare %slick.val @slick_rt_format_union_value(%slick.val)",
		"declare %slick.val @slick_rt_neg(%slick.val)",
		"declare %slick.val @slick_rt_not(%slick.val)",
		"declare %slick.val @slick_rt_add(%slick.val, %slick.val)",
		"declare %slick.val @slick_rt_sub(%slick.val, %slick.val)",
		"declare %slick.val @slick_rt_mul(%slick.val, %slick.val)",
		"declare %slick.val @slick_rt_cmp(%slick.val, %slick.val, i32)",
		"declare i32 @slick_rt_truth(%slick.val)",
		"declare void @slick_rt_check_cancel(ptr sret(%slick.out), ptr)",
		"declare i32 @slick_rt_is_control(i32)",
		"declare %slick.val @slick_rt_suppress(%slick.val, %slick.val)",
		"declare void @slick_rt_suppress_p(ptr, ptr, ptr)",
		"declare ptr @slick_rt_scope_new(ptr)",
		"declare %slick.val @slick_rt_task_start(ptr, ptr, ptr, i32, ptr)",
		"declare void @slick_rt_task_await(ptr sret(%slick.out), %slick.val)",
		"declare void @slick_rt_scope_finish(ptr sret(%slick.out), ptr, ptr)",
		"declare void @slick_rt_invoke(ptr sret(%slick.out), ptr, %slick.val, i32, ptr)",
		"declare void @slick_rt_iface_call(ptr sret(%slick.out), ptr, %slick.val, i32, i32, ptr)",
		"declare void @slick_rt_set_type_count(i32)",
		"declare void @slick_rt_set_type(i32, ptr, i32, ptr, ptr, ptr, i32, i32)",
		"declare void @slick_rt_set_union_count(i32)",
		"declare void @slick_rt_set_union(i32, ptr, i32, ptr, ptr)",
		"declare ptr @slick_rt_root_ctx()",
		"declare ptr @slick_rt_cleanup_ctx()",
		"declare void @slick_rt_print(%slick.val)",
		"declare void @slick_rt_write_bytes(%slick.val, i32)",
		"declare %slick.val @slick_rt_argv(i32, ptr)",
		"declare void @slick_rt_abort_missing(ptr)",
		"declare void @slick_rt_format_p(ptr, ptr)",
		"declare void @slick_nat_json_decode(ptr sret(%slick.out), ptr, ptr, ptr)",
		"declare void @slick_nat_json_encode(ptr sret(%slick.out), ptr, ptr, ptr)",
		"declare i32 @slick_rt_class_type_p(ptr)",
		"declare void @slick_rt_write_bytes_p(ptr, i32)",
		"declare void @slick_rt_not_p(ptr, ptr)",
		"declare void @slick_rt_neg_p(ptr, ptr)",
		"declare void @slick_rt_add_p(ptr, ptr, ptr)",
		"declare void @slick_rt_sub_p(ptr, ptr, ptr)",
		"declare void @slick_rt_mul_p(ptr, ptr, ptr)",
		"declare void @slick_rt_cmp_p(ptr, ptr, ptr, i32)",
		"declare void @slick_rt_map_get_p(ptr, ptr, ptr)",
		"declare void @slick_rt_map_with_p(ptr, ptr, ptr, ptr)",
		"declare void @slick_rt_map_without_p(ptr, ptr, ptr)",
		"declare i32 @slick_rt_map_contains_p(ptr, ptr)",
		"declare i64 @slick_rt_map_len_p(ptr)",
		"declare i64 @slick_rt_array_len_p(ptr)",
		"declare void @slick_rt_array_get_p(ptr, ptr, i64)",
		"declare void @slick_rt_array_index_p(ptr, ptr, i64)",
		"declare i32 @slick_rt_equal_p(ptr, ptr)",
		"declare i32 @slick_rt_truth_p(ptr)",
		"declare void @slick_rt_some_p(ptr, ptr)",
		"declare void @slick_rt_field_p(ptr, ptr, i32)",
		"declare void @slick_rt_optional_value_p(ptr, ptr)",
		"declare void @slick_rt_result_payload_p(ptr, ptr)",
		"declare void @slick_rt_union_field_p(ptr, ptr, i32)",
		"declare void @slick_rt_result_p(ptr, ptr, i32)",
		"declare i32 @slick_rt_result_ok_p(ptr)",
		"declare void @slick_rt_iter_item_p(ptr, ptr, i64)",
		"declare void @slick_rt_iter_at_p(ptr, ptr, i64, i32)",
		"declare void @slick_rt_iter_of_p(ptr, ptr)",
		"declare void @slick_rt_iter_enum_p(ptr, ptr)",
		"declare i64 @slick_rt_iter_len_p(ptr)",
		"declare void @slick_rt_print_p(ptr)",
	}
	for _, d := range decls {
		g.decl.WriteString(d + "\n")
	}
	for _, name := range nativeSymbols() {
		g.decl.WriteString("declare void @" + name + "(ptr sret(%slick.out), ptr, ptr)\n")
	}
	g.decl.WriteByte('\n')
}

func nativeSymbols() []string {
	return []string{
		"slick_nat_bytes_from_utf8", "slick_nat_bytes_to_utf8", "slick_nat_bytes_length",
		"slick_nat_bytes_at", "slick_nat_bytes_concat", "slick_nat_bytes_slice", "slick_nat_bytes_from_values",
		"slick_nat_utf8_decode_at", "slick_nat_unicode_is_letter", "slick_nat_unicode_is_digit",
		"slick_nat_unicode_is_space", "slick_nat_unicode_is_upper",
		"slick_nat_parse_int", "slick_nat_parse_float", "slick_nat_int_to_string", "slick_nat_float_to_string",
		"slick_nat_math_div", "slick_nat_math_rem",
		"slick_nat_env_get", "slick_nat_env_set", "slick_nat_env_unset",
		"slick_nat_fs_read_text", "slick_nat_fs_write_text", "slick_nat_fs_exists", "slick_nat_fs_mkdir",
		"slick_nat_fs_remove", "slick_nat_fs_read_dir", "slick_nat_fs_tmp", "slick_nat_fs_tmp_close",
		"slick_nat_path_join", "slick_nat_path_clean", "slick_nat_path_base", "slick_nat_path_dir",
		"slick_nat_path_ext", "slick_nat_path_abs",
		"slick_nat_text_trim", "slick_nat_text_contains", "slick_nat_text_starts", "slick_nat_text_ends",
		"slick_nat_text_split", "slick_nat_text_join", "slick_nat_text_replace", "slick_nat_text_cut", "slick_nat_text_quote",
		"slick_nat_io_reader", "slick_nat_io_writer", "slick_nat_io_read", "slick_nat_io_read_close",
		"slick_nat_io_write", "slick_nat_io_bytes", "slick_nat_io_write_close", "slick_nat_io_read_all", "slick_nat_io_copy",
		"slick_nat_process_run", "slick_nat_http_fetch", "slick_nat_http_header_values", "slick_nat_http_status_text",
		"slick_nat_http_serve",
		"slick_nat_sqlite_open", "slick_nat_sqlite_db_exec", "slick_nat_sqlite_db_query", "slick_nat_sqlite_db_begin",
		"slick_nat_sqlite_db_close", "slick_nat_sqlite_tx_exec", "slick_nat_sqlite_tx_query",
		"slick_nat_sqlite_tx_commit", "slick_nat_sqlite_tx_rollback", "slick_nat_sqlite_tx_close",
	}
}

func (g *llvmGen) intern(s string) string {
	if name, ok := g.strs[s]; ok {
		return name
	}
	name := fmt.Sprintf("@.str.%d", g.strN)
	g.strN++
	g.strs[s] = name
	escaped := strings.Builder{}
	for i := range len(s) {
		escaped.WriteString(fmt.Sprintf("\\%02X", s[i]))
	}
	fmt.Fprintf(&g.decl, "%s = private unnamed_addr constant [%d x i8] c\"%s\\00\"\n", name, len(s)+1, escaped.String())
	return name
}

func (g *llvmGen) emitUserFunctions() error {
	for _, name := range sortedKeys(g.program.functions) {
		if g.skipName(name) {
			continue
		}
		fn := g.program.functions[name]
		if fn.native != "" {
			if err := g.emitNativeWrapper(fn, "", nativeSymbol(fn.native)); err != nil {
				return err
			}
			continue
		}
		if err := g.emitFunction(fn, ""); err != nil {
			return err
		}
	}
	for _, className := range sortedKeys(g.program.classes) {
		if g.skipName(className) {
			continue
		}
		class := g.program.classes[className]
		for _, methodName := range sortedKeys(class.implementations) {
			impl := class.implementations[methodName]
			if impl.native != "" {
				if err := g.emitNativeWrapper(impl, className, nativeSymbol(impl.native)); err != nil {
					return err
				}
				continue
			}
			if err := g.emitFunction(impl, className); err != nil {
				return err
			}
		}
	}
	return nil
}

func nativeSymbol(n nativeFunction) string {
	switch n {
	case nativeStdBytesFromUtf8:
		return "slick_nat_bytes_from_utf8"
	case nativeStdBytesToUtf8:
		return "slick_nat_bytes_to_utf8"
	case nativeStdBytesLength:
		return "slick_nat_bytes_length"
	case nativeStdBytesAt:
		return "slick_nat_bytes_at"
	case nativeStdBytesConcat:
		return "slick_nat_bytes_concat"
	case nativeStdBytesSlice:
		return "slick_nat_bytes_slice"
	case nativeStdBytesFromValues:
		return "slick_nat_bytes_from_values"
	case nativeStdUTF8DecodeAt:
		return "slick_nat_utf8_decode_at"
	case nativeStdUnicodeIsLetter:
		return "slick_nat_unicode_is_letter"
	case nativeStdUnicodeIsDigit:
		return "slick_nat_unicode_is_digit"
	case nativeStdUnicodeIsWhitespace:
		return "slick_nat_unicode_is_space"
	case nativeStdUnicodeIsUpper:
		return "slick_nat_unicode_is_upper"
	case nativeStdConvertParseInt:
		return "slick_nat_parse_int"
	case nativeStdConvertParseFloat:
		return "slick_nat_parse_float"
	case nativeStdConvertIntToString:
		return "slick_nat_int_to_string"
	case nativeStdConvertFloatToString:
		return "slick_nat_float_to_string"
	case nativeStdMathDivide:
		return "slick_nat_math_div"
	case nativeStdMathRemainder:
		return "slick_nat_math_rem"
	case nativeStdEnvGet:
		return "slick_nat_env_get"
	case nativeStdEnvSet:
		return "slick_nat_env_set"
	case nativeStdEnvUnset:
		return "slick_nat_env_unset"
	case nativeStdFSReadText:
		return "slick_nat_fs_read_text"
	case nativeStdFSWriteText:
		return "slick_nat_fs_write_text"
	case nativeStdFSExists:
		return "slick_nat_fs_exists"
	case nativeStdFSCreateDirectoryAll:
		return "slick_nat_fs_mkdir"
	case nativeStdFSRemove:
		return "slick_nat_fs_remove"
	case nativeStdFSReadDirectory:
		return "slick_nat_fs_read_dir"
	case nativeStdFSCreateTemporaryDirectory:
		return "slick_nat_fs_tmp"
	case nativeStdFSTemporaryDirectoryClose:
		return "slick_nat_fs_tmp_close"
	case nativeStdPathJoin:
		return "slick_nat_path_join"
	case nativeStdPathClean:
		return "slick_nat_path_clean"
	case nativeStdPathBase:
		return "slick_nat_path_base"
	case nativeStdPathDirectory:
		return "slick_nat_path_dir"
	case nativeStdPathExtension:
		return "slick_nat_path_ext"
	case nativeStdPathIsAbsolute:
		return "slick_nat_path_abs"
	case nativeStdTextTrim:
		return "slick_nat_text_trim"
	case nativeStdTextContains:
		return "slick_nat_text_contains"
	case nativeStdTextStartsWith:
		return "slick_nat_text_starts"
	case nativeStdTextEndsWith:
		return "slick_nat_text_ends"
	case nativeStdTextSplit:
		return "slick_nat_text_split"
	case nativeStdTextJoin:
		return "slick_nat_text_join"
	case nativeStdTextReplaceAll:
		return "slick_nat_text_replace"
	case nativeStdTextCut:
		return "slick_nat_text_cut"
	case nativeStdTextQuote:
		return "slick_nat_text_quote"
	case nativeStdIOReaderFromBytes:
		return "slick_nat_io_reader"
	case nativeStdIOWriterToBytes:
		return "slick_nat_io_writer"
	case nativeStdIOReaderRead:
		return "slick_nat_io_read"
	case nativeStdIOReaderClose:
		return "slick_nat_io_read_close"
	case nativeStdIOWriterWrite:
		return "slick_nat_io_write"
	case nativeStdIOWriterBytes:
		return "slick_nat_io_bytes"
	case nativeStdIOWriterClose:
		return "slick_nat_io_write_close"
	case nativeStdIOReadAll:
		return "slick_nat_io_read_all"
	case nativeStdIOCopy:
		return "slick_nat_io_copy"
	case nativeStdProcessRun:
		return "slick_nat_process_run"
	case nativeStdHTTPFetch:
		return "slick_nat_http_fetch"
	case nativeStdHTTPHeaderValues:
		return "slick_nat_http_header_values"
	case nativeStdHTTPStatusText:
		return "slick_nat_http_status_text"
	case nativeStdHTTPServerServe:
		return "slick_nat_http_serve"
	case nativeStdSQLiteOpen:
		return "slick_nat_sqlite_open"
	case nativeStdSQLiteDatabaseExecute:
		return "slick_nat_sqlite_db_exec"
	case nativeStdSQLiteDatabaseQuery:
		return "slick_nat_sqlite_db_query"
	case nativeStdSQLiteDatabaseBegin:
		return "slick_nat_sqlite_db_begin"
	case nativeStdSQLiteDatabaseClose:
		return "slick_nat_sqlite_db_close"
	case nativeStdSQLiteTransactionExecute:
		return "slick_nat_sqlite_tx_exec"
	case nativeStdSQLiteTransactionQuery:
		return "slick_nat_sqlite_tx_query"
	case nativeStdSQLiteTransactionCommit:
		return "slick_nat_sqlite_tx_commit"
	case nativeStdSQLiteTransactionRollback:
		return "slick_nat_sqlite_tx_rollback"
	case nativeStdSQLiteTransactionClose:
		return "slick_nat_sqlite_tx_close"
	default:
		return ""
	}
}

func (g *llvmGen) symbol(fn *functionDecl, receiver string) string {
	if receiver != "" {
		return llvmMethodSymbol(receiver, fn.name)
	}
	return llvmFunctionSymbol(fn.qualified)
}

func (g *llvmGen) emitNativeWrapper(fn *functionDecl, receiver, native string) error {
	jsonSchema := ""
	if fn.native == nativeStdJsonDecode {
		result := g.program.resolveType(fn.namespace, fn.aliases, fn.result)
		target, _, ok := resultTypeArgs(result)
		if !ok {
			return fmt.Errorf("invalid instantiated JSON decode result %s", result)
		}
		native, jsonSchema = "slick_nat_json_decode", g.jsonSchema(target)
	} else if fn.native == nativeStdJsonEncode {
		if len(fn.params) != 1 {
			return fmt.Errorf("invalid instantiated JSON encode parameters")
		}
		target := g.program.resolveType(fn.namespace, fn.aliases, fn.params[0].typ)
		native, jsonSchema = "slick_nat_json_encode", g.jsonSchema(target)
	} else if native == "" {
		if isNativeStdBuffer(fn.native) {
			return nil
		}
		return fmt.Errorf("unknown native Slick function %s", fn.native)
	}
	sym := g.symbol(fn, receiver)
	fmt.Fprintf(&g.out, "define void @%s(ptr sret(%%slick.out) %%ret, ptr %%ctx, ptr %%args) {\n", sym)
	g.out.WriteString("  %cslot = alloca %slick.out\n")
	g.out.WriteString("  call void @slick_rt_check_cancel(ptr sret(%slick.out) %cslot, ptr %ctx)\n")
	g.out.WriteString("  %c = load %slick.out, ptr %cslot\n")
	g.out.WriteString("  %cc = extractvalue %slick.out %c, 0\n")
	g.out.WriteString("  %cnz = icmp ne i64 %cc, 0\n")
	g.out.WriteString("  br i1 %cnz, label %cancel, label %go\ncancel:\n  store %slick.out %c, ptr %ret\n  ret void\ngo:\n")
	g.out.WriteString("  %rslot = alloca %slick.out\n")
	if jsonSchema != "" {
		fmt.Fprintf(&g.out, "  call void @%s(ptr sret(%%slick.out) %%rslot, ptr %%ctx, ptr %%args, ptr %s)\n", native, g.intern(jsonSchema))
	} else {
		fmt.Fprintf(&g.out, "  call void @%s(ptr sret(%%slick.out) %%rslot, ptr %%ctx, ptr %%args)\n", native)
	}
	g.out.WriteString("  %r = load %slick.out, ptr %rslot\n")
	if fn.native == nativeStdIOReaderFromBytes {
		vtable := g.ifaceVTable(stdIOReaderName, stdIOBytesReaderName)
		g.out.WriteString("  %rcode = extractvalue %slick.out %r, 0\n")
		g.out.WriteString("  %rok = icmp eq i64 %rcode, 0\n")
		g.out.WriteString("  br i1 %rok, label %wrap, label %raw\nwrap:\n")
		g.out.WriteString("  %rv = extractvalue %slick.out %r, 1\n")
		fmt.Fprintf(&g.out, "  %%iface = call %%slick.val @slick_rt_iface(i32 %d, %%slick.val %%rv, i32 %d, ptr %s)\n", g.typeID[stdIOReaderName], len(g.ifaceMethods[stdIOReaderName]), vtable)
		g.out.WriteString("  %wrapped = insertvalue %slick.out %r, %slick.val %iface, 1\n")
		g.out.WriteString("  store %slick.out %wrapped, ptr %ret\n  ret void\nraw:\n")
	}
	g.out.WriteString("  store %slick.out %r, ptr %ret\n  ret void\n}\n\n")
	return nil
}

func (g *llvmGen) emitFunction(fn *functionDecl, receiver string) error {
	g.fn = &llvmFn{cur: "entry"}
	scope := &llvmScope{function: fn, locals: map[string]llvmBind{}, pending: map[string]string{}}
	off := 0
	if receiver != "" {
		g.setLocal(scope, "self", receiver, g.loadArg(0))
		off = 1
	}
	for i, param := range fn.params {
		typ := g.program.resolveType(fn.namespace, fn.aliases, param.typ)
		g.setLocal(scope, param.name, typ, g.loadArg(off+i))
	}
	result := g.program.resolveType(fn.namespace, fn.aliases, fn.result)
	if err := g.emitCallableBody(fn, scope, result); err != nil {
		return err
	}
	fmt.Fprintf(&g.out, "define void @%s(ptr sret(%%slick.out) %%ret, ptr %%ctx, ptr %%args) {\nentry:\n%s}\n\n", g.symbol(fn, receiver), g.fn.body.String())
	return nil
}

func (g *llvmGen) loadArg(i int) string {
	p := g.reg()
	v := g.reg()
	fmt.Fprintf(&g.fn.body, "  %s = getelementptr %%slick.val, ptr %%args, i32 %d\n", p, i)
	fmt.Fprintf(&g.fn.body, "  %s = load %%slick.val, ptr %s\n", v, p)
	return v
}

func (g *llvmGen) reg() string {
	g.fn.next++
	return fmt.Sprintf("%%t%d", g.fn.next)
}

func (g *llvmGen) label(prefix string) string {
	g.fn.block++
	return fmt.Sprintf("%s%d", prefix, g.fn.block)
}

func (g *llvmGen) emit(format string, args ...any) {
	if format == "%s:" && len(args) == 1 {
		if name, ok := args[0].(string); ok {
			g.fn.cur = name
		}
	}
	fmt.Fprintf(&g.fn.body, format+"\n", args...)
}

func (g *llvmGen) callVal(fn string, args string) string {
	r := g.reg()
	g.emit("  %s = call %%slick.val @%s(%s)", r, fn, args)
	return r
}

func (g *llvmGen) ptrCall(fn string, vals []string, extra string) string {
	out := g.reg()
	g.emit("  %s = alloca %%slick.val, align 8", out)
	parts := []string{"ptr " + out}
	for _, v := range vals {
		p := g.reg()
		g.emit("  %s = alloca %%slick.val, align 8", p)
		g.emit("  store %%slick.val %s, ptr %s, align 8", v, p)
		parts = append(parts, "ptr "+p)
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	g.emit("  call void @%s(%s)", fn, strings.Join(parts, ", "))
	r := g.reg()
	g.emit("  %s = load %%slick.val, ptr %s, align 8", r, out)
	return r
}

func (g *llvmGen) callOut(fn, args string) (code, value string) {
	slot := g.reg()
	g.emit("  %s = alloca %%slick.out", slot)
	if args == "" {
		g.emit("  call void @%s(ptr sret(%%slick.out) %s)", fn, slot)
	} else {
		g.emit("  call void @%s(ptr sret(%%slick.out) %s, %s)", fn, slot, args)
	}
	r := g.reg()
	g.emit("  %s = load %%slick.out, ptr %s", r, slot)
	raw := g.reg()
	code = g.reg()
	value = g.reg()
	g.emit("  %s = extractvalue %%slick.out %s, 0", raw, r)
	g.emit("  %s = trunc i64 %s to i32", code, raw)
	g.emit("  %s = extractvalue %%slick.out %s, 1", value, r)
	return code, value
}

func (g *llvmGen) packOut(code, value string) string {
	wide := code
	if !strings.HasPrefix(code, "%") || true {
		w := g.reg()
		g.emit("  %s = zext i32 %s to i64", w, code)
		wide = w
	}
	z := g.reg()
	g.emit("  %s = insertvalue %%slick.out undef, i64 %s, 0", z, wide)
	o := g.reg()
	g.emit("  %s = insertvalue %%slick.out %s, %%slick.val %s, 1", o, z, value)
	return o
}

func (g *llvmGen) retOut(code, value string) {
	packed := g.packOut(code, value)
	g.emit("  store %%slick.out %s, ptr %%ret", packed)
	g.emit("  ret void")
}

func (g *llvmGen) zero() string {
	return g.callVal("slick_rt_none", "") // unused; real zero is null
}

func (g *llvmGen) null() string {
	r := g.reg()
	g.emit("  %s = insertvalue %%slick.val { i64 0, i64 0 }, i64 0, 0", r)
	return r
}

func (g *llvmGen) emitCallableBody(fn *functionDecl, scope *llvmScope, result string) error {
	if g.program.usesContext {
		code, val := g.callOut("slick_rt_check_cancel", "ptr %ctx")
		nz := g.reg()
		g.emit("  %s = icmp ne i32 %s, 0", nz, code)
		fail, ok := g.label("ccfail"), g.label("ccok")
		g.emit("  br i1 %s, label %%%s, label %%%s", nz, fail, ok)
		g.emit("%s:", fail)
		g.retOut(code, val)
		g.emit("%s:", ok)
	}
	val, code, err := g.emitBlock(fn.ast, scope, result, "")
	if err != nil {
		return err
	}
	isRet := g.reg()
	g.emit("  %s = icmp eq i32 %s, %d", isRet, code, slickCodeReturn)
	yes, no := g.label("retu"), g.label("retn")
	g.emit("  br i1 %s, label %%%s, label %%%s", isRet, yes, no)
	g.emit("%s:", yes)
	g.retOut(strconv.Itoa(slickCodeOK), val)
	g.emit("%s:", no)
	g.retOut(code, val)
	return nil
}

func (g *llvmGen) emitBlock(block *blockNode, scope *llvmScope, result, prelude string) (string, string, error) {
	if prelude != "" {
		g.fn.body.WriteString(prelude)
	}
	if block != nil && block.hasAsync {
		scope.taskScope = g.reg()
		g.emit("  %s = call ptr @slick_rt_scope_new(ptr %%ctx)", scope.taskScope)
	}
	if block == nil || len(block.statements) == 0 {
		return g.null(), strconv.Itoa(slickCodeOK), nil
	}
	var lastVal, lastCode, valueSlot, codeSlot, end string
	for i, stmt := range block.statements {
		v, c, err := g.emitStatement(stmt, scope, result, i == len(block.statements)-1)
		if err != nil {
			return "", "", err
		}
		lastVal, lastCode = v, c
		if i == len(block.statements)-1 || c == strconv.Itoa(slickCodeOK) {
			continue
		}
		if _, err := strconv.Atoi(c); err == nil {
			break
		}
		if end == "" {
			valueSlot, codeSlot, end = g.reg(), g.reg(), g.label("blockend")
			g.emit("  %s = alloca %%slick.val, align 8", valueSlot)
			g.emit("  %s = alloca i32, align 4", codeSlot)
		}
		stop, next := g.label("blockstop"), g.label("blocknext")
		nz := g.reg()
		g.emit("  %s = icmp ne i32 %s, 0", nz, c)
		g.emit("  br i1 %s, label %%%s, label %%%s", nz, stop, next)
		g.emit("%s:", stop)
		g.emit("  store %%slick.val %s, ptr %s, align 8", v, valueSlot)
		g.emit("  store i32 %s, ptr %s, align 4", c, codeSlot)
		g.emit("  br label %%%s", end)
		g.emit("%s:", next)
	}
	if end != "" {
		g.emit("  store %%slick.val %s, ptr %s, align 8", lastVal, valueSlot)
		g.emit("  store i32 %s, ptr %s, align 4", lastCode, codeSlot)
		g.emit("  br label %%%s", end)
		g.emit("%s:", end)
		lastVal, lastCode = g.reg(), g.reg()
		g.emit("  %s = load %%slick.val, ptr %s, align 8", lastVal, valueSlot)
		g.emit("  %s = load i32, ptr %s, align 4", lastCode, codeSlot)
	}
	if block.hasAsync {
		packed := g.packOut(lastCode, lastVal)
		pslot := g.reg()
		g.emit("  %s = alloca %%slick.out", pslot)
		g.emit("  store %%slick.out %s, ptr %s", packed, pslot)
		fin := g.reg()
		g.emit("  %s = alloca %%slick.out", fin)
		g.emit("  call void @slick_rt_scope_finish(ptr sret(%%slick.out) %s, ptr %s, ptr %s)", fin, scope.taskScope, pslot)
		loaded := g.reg()
		g.emit("  %s = load %%slick.out, ptr %s", loaded, fin)
		rawCode := g.reg()
		lastCode, lastVal = g.reg(), g.reg()
		g.emit("  %s = extractvalue %%slick.out %s, 0", rawCode, loaded)
		g.emit("  %s = trunc i64 %s to i32", lastCode, rawCode)
		g.emit("  %s = extractvalue %%slick.out %s, 1", lastVal, loaded)
	}
	return lastVal, lastCode, nil
}

func (g *llvmGen) emitStatement(stmt statementNode, scope *llvmScope, result string, last bool) (string, string, error) {
	switch node := stmt.(type) {
	case *letStatement:
		v, c, err := g.emitExpr(node.value, scope)
		if err != nil {
			return "", "", err
		}
		typ, err := g.exprType(node.value, scope)
		if err != nil {
			return "", "", err
		}
		if len(node.names) == 1 {
			g.setLocal(scope, node.names[0], typ, v)
		} else {
			elems, _ := tupleElementTypes(typ)
			for i, name := range node.names {
				if name == "_" {
					continue
				}
				item := g.ptrCall("slick_rt_array_index_p", []string{v}, fmt.Sprintf("i64 %d", i))
				g.setLocal(scope, name, elems[i], item)
			}
		}
		if last {
			return g.statementResult(c, v, g.null()), c, nil
		}
		return v, c, nil
	case *assignmentStatement:
		bind, ok := scope.locals[node.name]
		if !ok {
			return "", "", fmt.Errorf("unknown generated binding %s", node.name)
		}
		v, c, err := g.emitExpr(node.value, scope)
		if err != nil {
			return "", "", err
		}
		from, err := g.exprType(node.value, scope)
		if err != nil {
			return "", "", err
		}
		converted := g.convert(v, from, bind.declared)
		g.setLocal(scope, node.name, bind.declared, converted)
		if last {
			return g.statementResult(c, v, g.null()), c, nil
		}
		return converted, c, nil

	case *asyncLetStatement:
		if err := g.emitAsyncLet(node, scope, result); err != nil {
			return "", "", err
		}
		if last {
			return g.null(), strconv.Itoa(slickCodeOK), nil
		}
		return g.null(), strconv.Itoa(slickCodeOK), nil
	case *forStatement:
		if err := g.emitFor(node, scope, result); err != nil {
			return "", "", err
		}
		if last {
			return g.null(), strconv.Itoa(slickCodeOK), nil
		}
		return g.null(), strconv.Itoa(slickCodeOK), nil
	case *breakStatement:
		return g.null(), strconv.Itoa(slickCodeBreak), nil
	case *continueStatement:
		return g.null(), strconv.Itoa(slickCodeContinue), nil

	case *throwStatement:
		v, c, err := g.emitExpr(node.value, scope)
		if err != nil {
			return "", "", err
		}
		if err := g.failIf(c, v); err != nil {
			return "", "", err
		}
		return v, strconv.Itoa(slickCodeThrow), nil
	case *returnStatement:
		v, c, err := g.emitExpr(node.value, scope)
		if err != nil {
			return "", "", err
		}
		if err := g.failIf(c, v); err != nil {
			return "", "", err
		}
		from, err := g.exprType(node.value, scope)
		if err != nil {
			return "", "", err
		}
		declared := g.program.resolveType(scope.function.namespace, scope.function.aliases, scope.function.result)
		return g.convert(v, from, declared), strconv.Itoa(slickCodeReturn), nil
	case *expressionStatement:
		v, c, err := g.emitExpr(node.value, scope)
		if err != nil {
			return "", "", err
		}
		if c != strconv.Itoa(slickCodeOK) {
			return v, c, nil
		}
		actual, err := g.exprType(node.value, scope)
		if err != nil {
			return "", "", err
		}
		if last && actual != typeNever && g.program.assignable(actual, result) {
			return g.convert(v, actual, result), strconv.Itoa(slickCodeOK), nil
		}
		if last {
			return g.null(), strconv.Itoa(slickCodeOK), nil
		}
		return v, strconv.Itoa(slickCodeOK), nil
	default:
		return "", "", fmt.Errorf("unsupported generated statement %T", stmt)
	}
}

func (g *llvmGen) statementResult(code, failure, success string) string {
	if code == strconv.Itoa(slickCodeOK) {
		return success
	}
	isFailure := g.reg()
	g.emit("  %s = icmp ne i32 %s, 0", isFailure, code)
	fail, ok, done := g.label("stfail"), g.label("stok"), g.label("stdone")
	g.emit("  br i1 %s, label %%%s, label %%%s", isFailure, fail, ok)
	g.emit("%s:", fail)
	g.emit("  br label %%%s", done)
	g.emit("%s:", ok)
	g.emit("  br label %%%s", done)
	g.emit("%s:", done)
	value := g.reg()
	g.emit("  %s = phi %%slick.val [ %s, %%%s ], [ %s, %%%s ]", value, failure, fail, success, ok)
	return value
}

func (g *llvmGen) failIf(code, value string) error {
	if code == strconv.Itoa(slickCodeOK) {
		return nil
	}
	nz := g.reg()
	g.emit("  %s = icmp ne i32 %s, 0", nz, code)
	fail, ok := g.label("fail"), g.label("ok")
	g.emit("  br i1 %s, label %%%s, label %%%s", nz, fail, ok)
	g.emit("%s:", fail)
	isReturn := g.reg()
	g.emit("  %s = icmp eq i32 %s, %d", isReturn, code, slickCodeReturn)
	ret, other := g.label("failret"), g.label("failout")
	g.emit("  br i1 %s, label %%%s, label %%%s", isReturn, ret, other)
	g.emit("%s:", ret)
	g.retOut(strconv.Itoa(slickCodeOK), value)
	g.emit("%s:", other)
	g.retOut(code, value)
	g.emit("%s:", ok)
	return nil
}

func (g *llvmGen) emitFor(node *forStatement, scope *llvmScope, result string) error {
	seq, c, err := g.emitExpr(node.iterable, scope)
	if err != nil {
		return err
	}
	if err := g.failIf(c, seq); err != nil {
		return err
	}
	iter := g.ptrCall("slick_rt_iter_of_p", []string{seq}, "")
	np := g.reg()
	g.emit("  %s = alloca %%slick.val, align 8", np)
	g.emit("  store %%slick.val %s, ptr %s, align 8", iter, np)
	n := g.reg()
	g.emit("  %s = call i64 @slick_rt_iter_len_p(ptr %s)", n, np)
	idx := g.reg()
	g.emit("  %s = alloca i64\n  store i64 0, ptr %s", idx, idx)
	head, body, done := g.label("forh"), g.label("forb"), g.label("ford")
	g.emit("  br label %%%s", head)
	g.emit("%s:", head)
	cur := g.reg()
	g.emit("  %s = load i64, ptr %s", cur, idx)
	cmp := g.reg()
	g.emit("  %s = icmp slt i64 %s, %s", cmp, cur, n)
	g.emit("  br i1 %s, label %%%s, label %%%s", cmp, body, done)
	g.emit("%s:", body)
	if g.program.usesContext {
		cc, cv := g.callOut("slick_rt_check_cancel", "ptr %ctx")
		if err := g.failIf(cc, cv); err != nil {
			return err
		}
	}
	loop := scope.clone()
	iterType, err := g.exprType(node.iterable, scope)
	if err != nil {
		return err
	}
	elem, _ := iterableElementType(iterType)
	bindTypes := []string{elem}
	if len(node.bindings) > 1 {
		bindTypes, _ = tupleElementTypes(elem)
	}
	for i, name := range node.bindings {
		if name == "_" {
			continue
		}
		var item string
		if len(node.bindings) == 1 {
			item = g.ptrCall("slick_rt_iter_item_p", []string{iter}, "i64 "+cur)
		} else {
			item = g.ptrCall("slick_rt_iter_at_p", []string{iter}, fmt.Sprintf("i64 %s, i32 %d", cur, i))
		}
		g.setLocal(loop, name, bindTypes[i], item)
	}

	bodyValue, code, err := g.emitBlock(node.body, loop, "null", "")
	if err != nil {
		return err
	}
	isBreak := g.reg()
	g.emit("  %s = icmp eq i32 %s, %d", isBreak, code, slickCodeBreak)
	brk, notBrk := g.label("brk"), g.label("nbrk")
	g.emit("  br i1 %s, label %%%s, label %%%s", isBreak, brk, notBrk)
	g.emit("%s:", brk)
	g.emit("  br label %%%s", done)
	g.emit("%s:", notBrk)
	isCont := g.reg()
	g.emit("  %s = icmp eq i32 %s, %d", isCont, code, slickCodeContinue)
	cont, notCont := g.label("cont"), g.label("ncont")
	g.emit("  br i1 %s, label %%%s, label %%%s", isCont, cont, notCont)
	g.emit("%s:", notCont)
	if err := g.failIf(code, bodyValue); err != nil {
		return err
	}
	g.emit("  br label %%%s", cont)
	g.emit("%s:", cont)
	next := g.reg()
	g.emit("  %s = add i64 %s, 1", next, cur)
	g.emit("  store i64 %s, ptr %s", next, idx)
	g.emit("  br label %%%s", head)

	g.emit("%s:", done)
	return nil
}

func (g *llvmGen) emitAsyncLet(node *asyncLetStatement, scope *llvmScope, result string) error {
	if scope.taskScope == "" {
		return fmt.Errorf("async let has no generated task scope")
	}
	cc, cv := g.callOut("slick_rt_check_cancel", "ptr %ctx")
	if err := g.failIf(cc, cv); err != nil {
		return err
	}
	call := node.call
	args := make([]string, 0, len(call.args)+1)
	if name, ok := call.callee.(*nameExpression); ok && call.resolvedReceiver != "" {
		parts := strings.Split(name.name, ".")
		if len(parts) == 2 {
			receiver, exists := scope.locals[parts[0]]
			if !exists {
				return fmt.Errorf("unknown generated async receiver %s", parts[0])
			}
			args = append(args, g.loadBind(receiver))
		}
	}
	for i, arg := range call.args {
		v, c, err := g.emitExpr(arg, scope)
		if err != nil {
			return err
		}
		if err := g.failIf(c, v); err != nil {
			return err
		}
		if i < len(call.resolvedParams) {
			v = g.convert(v, call.resolvedArgumentTypes[i], call.resolvedParams[i])
		}
		args = append(args, v)
	}
	slot := g.reg()
	g.emit("  %s = alloca [8 x %%slick.val]", slot)
	for i, a := range args {
		p := g.reg()
		g.emit("  %s = getelementptr [8 x %%slick.val], ptr %s, i32 0, i32 %d", p, slot, i)
		g.emit("  store %%slick.val %s, ptr %s", a, p)
	}
	var target string
	var err error
	if call.resolvedNative == nativeStdJsonDecode || call.resolvedNative == nativeStdJsonEncode {
		target, err = g.jsonTaskTarget(call)
	} else {
		target, err = g.callTargetName(call, scope)
	}
	if err != nil {
		return err
	}
	task := g.callVal("slick_rt_task_start", fmt.Sprintf("ptr %%ctx, ptr %s, ptr @%s, i32 %d, ptr %s", scope.taskScope, target, len(args), slot))
	scope.pending[node.name] = task
	return nil
}

func (g *llvmGen) jsonTaskTarget(call *callExpression) (string, error) {
	if len(call.resolvedTypeArgs) != 1 {
		return "", fmt.Errorf("generated async JSON call requires one concrete type argument")
	}
	helper, operation := "slick_nat_json_decode", "decode"
	if call.resolvedNative == nativeStdJsonEncode {
		helper, operation = "slick_nat_json_encode", "encode"
	}
	schema := g.jsonSchema(call.resolvedTypeArgs[0])
	key := operation + "\x00" + schema
	if symbol, ok := g.jsonTasks[key]; ok {
		return symbol, nil
	}
	symbol := "slick_json_task_" + hexName(key)
	g.jsonTasks[key] = symbol
	schemaPtr := g.intern(schema)
	fmt.Fprintf(&g.decl, "define void @%s(ptr sret(%%slick.out) %%ret, ptr %%ctx, ptr %%args) {\n", symbol)
	g.decl.WriteString("  %slot = alloca %slick.out\n")
	fmt.Fprintf(&g.decl, "  call void @%s(ptr sret(%%slick.out) %%slot, ptr %%ctx, ptr %%args, ptr %s)\n", helper, schemaPtr)
	g.decl.WriteString("  %out = load %slick.out, ptr %slot\n")
	g.decl.WriteString("  store %slick.out %out, ptr %ret\n")
	g.decl.WriteString("  ret void\n}\n\n")
	return symbol, nil
}

func (g *llvmGen) callTargetName(call *callExpression, scope *llvmScope) (string, error) {
	name, ok := call.callee.(*nameExpression)
	if !ok {
		return "", fmt.Errorf("generated async call target is not a name")
	}
	parts := strings.Split(name.name, ".")
	if len(parts) == 2 && call.resolvedReceiver != "" {
		return llvmMethodSymbol(call.resolvedReceiver, parts[1]), nil
	}
	fn := g.program.callTarget(scope.function, call, name.name)
	if fn == nil {
		return "", fmt.Errorf("unknown generated async function %s", name.name)
	}
	return llvmFunctionSymbol(fn.qualified), nil
}

func (g *llvmGen) emitExpr(expr expressionNode, scope *llvmScope) (string, string, error) {
	ok := strconv.Itoa(slickCodeOK)
	switch node := expr.(type) {
	case *literalExpression:
		return g.literal(node.value), ok, nil
	case *tupleExpression:
		typ, err := g.exprType(expr, scope)
		if err != nil {
			return "", "", err
		}
		elems, _ := tupleElementTypes(typ)
		vals := make([]string, len(node.elements))
		for i, el := range node.elements {
			v, c, err := g.emitExpr(el, scope)
			if err != nil {
				return "", "", err
			}
			if err := g.failIf(c, v); err != nil {
				return "", "", err
			}
			from, _ := g.exprType(el, scope)
			vals[i] = g.convert(v, from, elems[i])
		}
		return g.packArray(7, vals), ok, nil
	case *arrayExpression:
		typ, err := g.exprType(expr, scope)
		if err != nil {
			return "", "", err
		}
		elem, _ := arrayElementType(typ)
		vals := make([]string, len(node.elements))
		for i, el := range node.elements {
			v, c, err := g.emitExpr(el, scope)
			if err != nil {
				return "", "", err
			}
			if err := g.failIf(c, v); err != nil {
				return "", "", err
			}
			from, _ := g.exprType(el, scope)
			vals[i] = g.convert(v, from, elem)
		}
		return g.packArray(6, vals), ok, nil
	case *mapExpression:
		typ, err := g.exprType(expr, scope)
		if err != nil {
			return "", "", err
		}
		keyT, valT, _ := mapTypeArgs(typ)
		if len(node.entries) == 0 {
			return g.callVal("slick_rt_empty_map", ""), ok, nil
		}
		keys, vals := make([]string, len(node.entries)), make([]string, len(node.entries))
		for i, e := range node.entries {
			k, c, err := g.emitExpr(e.key, scope)
			if err != nil {
				return "", "", err
			}
			if err := g.failIf(c, k); err != nil {
				return "", "", err
			}
			v, c, err := g.emitExpr(e.value, scope)
			if err != nil {
				return "", "", err
			}
			if err := g.failIf(c, v); err != nil {
				return "", "", err
			}
			kf, _ := g.exprType(e.key, scope)
			vf, _ := g.exprType(e.value, scope)
			keys[i] = g.convert(k, kf, keyT)
			vals[i] = g.convert(v, vf, valT)
		}
		kp, vp := g.packVals(keys), g.packVals(vals)
		return g.callVal("slick_rt_map", fmt.Sprintf("i64 %d, ptr %s, ptr %s", len(keys), kp, vp)), ok, nil
	case *rangeExpression:
		s, c, err := g.emitExpr(node.start, scope)
		if err != nil {
			return "", "", err
		}
		if err := g.failIf(c, s); err != nil {
			return "", "", err
		}
		e, c, err := g.emitExpr(node.end, scope)
		if err != nil {
			return "", "", err
		}
		if err := g.failIf(c, e); err != nil {
			return "", "", err
		}
		si, ei := g.reg(), g.reg()
		g.emit("  %s = extractvalue %%slick.val %s, 1", si, s)
		g.emit("  %s = extractvalue %%slick.val %s, 1", ei, e)
		return g.callVal("slick_rt_iter_range", fmt.Sprintf("i64 %s, i64 %s", si, ei)), ok, nil
	case *templateExpression:
		text, err := g.template(node.text, scope)
		if err != nil {
			return "", "", err
		}
		return text, ok, nil
	case *nameExpression:
		v, err := g.nameValue(node, scope)
		if err != nil {
			return "", "", err
		}
		return v, ok, nil
	case *lambdaExpression:
		v, err := g.emitLambda(node, scope)
		if err != nil {
			return "", "", err
		}
		return v, ok, nil
	case *objectExpression:
		typ, err := g.exprType(expr, scope)
		if err != nil {
			return "", "", err
		}
		v, err := g.emitObject(node, scope, typ)
		if err != nil {
			return "", "", err
		}
		return v, ok, nil
	case *callExpression:
		return g.emitCall(node, scope)
	case *awaitExpression:
		task, okp := scope.pending[node.name]
		if !okp {
			return "", "", fmt.Errorf("unknown generated pending binding %s", node.name)
		}
		code, val := g.callOut("slick_rt_task_await", "%slick.val "+task)
		return val, code, nil
	case *unaryExpression:
		v, c, err := g.emitExpr(node.value, scope)
		if err != nil {
			return "", "", err
		}
		if err := g.failIf(c, v); err != nil {
			return "", "", err
		}
		if node.op == "!" {
			return g.ptrCall("slick_rt_not_p", []string{v}, ""), ok, nil
		}
		return g.ptrCall("slick_rt_neg_p", []string{v}, ""), ok, nil
	case *binaryExpression:
		return g.emitBinary(node, scope)
	case *ifExpression:
		return g.emitIf(node, scope)
	case *catchExpression:
		return g.emitCatch(node, scope)
	case *resultExpression:
		return g.emitResult(node, scope)
	case *propagateExpression:
		return g.emitPropagate(node, scope)
	case *usingExpression:
		return g.emitUsing(node, scope)
	case *matchExpression:
		return g.emitMatch(node, scope)
	default:
		return "", "", fmt.Errorf("unsupported generated expression %T", expr)
	}
}

func (g *llvmGen) literal(v any) string {
	switch value := v.(type) {
	case nil:
		return g.null()
	case bool:
		n := 0
		if value {
			n = 1
		}
		return g.callVal("slick_rt_bool", fmt.Sprintf("i32 %d", n))
	case int64:
		return g.callVal("slick_rt_int", fmt.Sprintf("i64 %d", value))
	case float64:
		return g.callVal("slick_rt_float", fmt.Sprintf("double 0x%016X", math.Float64bits(value)))
	case string:
		return g.callVal("slick_rt_string", fmt.Sprintf("ptr %s, i64 %d", g.intern(value), len(value)))
	default:
		return g.null()
	}
}

func (g *llvmGen) packVals(vals []string) string {
	slot := g.reg()
	g.emit("  %s = alloca [%d x %%slick.val]", slot, max(1, len(vals)))
	for i, v := range vals {
		p := g.reg()
		g.emit("  %s = getelementptr [%d x %%slick.val], ptr %s, i32 0, i32 %d", p, max(1, len(vals)), slot, i)
		g.emit("  store %%slick.val %s, ptr %s", v, p)
	}
	return slot
}

func (g *llvmGen) packArray(kind int, vals []string) string {
	return g.callVal("slick_rt_array", fmt.Sprintf("i32 %d, i64 %d, ptr %s", kind, len(vals), g.packVals(vals)))
}

func (g *llvmGen) nameValue(node *nameExpression, scope *llvmScope) (string, error) {
	parts := strings.Split(node.name, ".")
	bind, ok := scope.locals[parts[0]]
	if !ok {
		if union, variant, named := g.program.resolveVariant(scope.function.namespace, scope.function.aliases, node.name); named && variant != nil {
			id := g.unionID[union.qualified]
			return g.callVal("slick_rt_union", fmt.Sprintf("i32 %d, i32 %d, i32 0, ptr null", id, variant.tag)), nil
		}
		if decl := g.program.constantFor(scope.function.namespace, scope.function.aliases, node.name); decl != nil {
			if decl.evaluated {
				if variant, ok := decl.value.(constantVariant); ok {
					id := g.unionID[variant.union.qualified]
					return g.callVal("slick_rt_union", fmt.Sprintf("i32 %d, i32 %d, i32 0, ptr null", id, variant.variant.tag)), nil
				}
				return g.literal(decl.value), nil
			}
		}
		if function := g.program.resolveFunction(scope.function, node.name); function != nil {
			return g.callVal("slick_rt_callable", fmt.Sprintf("ptr @%s, i32 0, ptr null, i32 %d", llvmFunctionSymbol(function.qualified), len(function.params))), nil
		}
		return "", fmt.Errorf("unknown generated value %s", node.name)
	}
	value, typ := g.loadBind(bind), bind.typ

	for _, field := range parts[1:] {
		class := g.program.classes[typ]
		if class == nil {
			return "", fmt.Errorf("%s has no generated fields", typ)
		}
		idx, exists := g.fieldIdx[typ][field]
		if !exists {
			return "", fmt.Errorf("%s has no generated field %s", typ, field)
		}
		value = g.ptrCall("slick_rt_field_p", []string{value}, fmt.Sprintf("i32 %d", idx))
		typ = g.program.resolveType(class.namespace, class.aliases, class.fields[field].typ)
	}
	return value, nil
}

func (g *llvmGen) emitObject(node *objectExpression, scope *llvmScope, typ string) (string, error) {
	class := g.program.classes[typ]
	if class == nil {
		return "", fmt.Errorf("unknown generated class %s", typ)
	}
	names := sortedKeys(class.fields)
	vals := make([]string, len(names))
	provided := map[string]string{}
	for _, field := range node.fields {
		v, c, err := g.emitExpr(field.value, scope)
		if err != nil {
			return "", err
		}
		if err := g.failIf(c, v); err != nil {
			return "", err
		}
		from, _ := g.exprType(field.value, scope)
		declared := g.program.resolveType(class.namespace, class.aliases, class.fields[field.name].typ)
		provided[field.name] = g.convert(v, from, declared)
	}
	for i, name := range names {
		if v, ok := provided[name]; ok {
			vals[i] = v
		} else {
			vals[i] = g.null()
		}
	}
	id := g.typeID[typ]
	return g.callVal("slick_rt_class", fmt.Sprintf("i32 %d, i32 %d, ptr %s", id, len(vals), g.packVals(vals))), nil
}

func (g *llvmGen) emitLambda(node *lambdaExpression, scope *llvmScope) (string, error) {
	if node.fn == nil {
		return "", fmt.Errorf("generated lambda has no checked signature")
	}
	caps := make([]string, 0, len(node.captures))
	for _, name := range node.captures {
		bind, ok := scope.locals[name]
		if !ok {
			return "", fmt.Errorf("unknown generated capture %s", name)
		}
		caps = append(caps, g.loadBind(bind))
	}
	sym := fmt.Sprintf("slick_lambda_%d", g.next)
	g.next++
	saved := g.fn
	g.fn = &llvmFn{cur: "entry"}
	lscope := &llvmScope{function: node.fn, locals: map[string]llvmBind{}, pending: map[string]string{}}
	for i, name := range node.captures {
		bind := scope.locals[name]
		g.setLocal(lscope, name, bind.typ, g.loadArg(i))
	}
	for i, param := range node.params {
		typ := g.program.resolveType(node.fn.namespace, node.fn.aliases, param.typ)
		g.setLocal(lscope, param.name, typ, g.loadArg(len(node.captures)+i))
	}
	result := g.program.resolveType(node.fn.namespace, node.fn.aliases, node.fn.result)
	if err := g.emitCallableBody(node.fn, lscope, result); err != nil {
		return "", err
	}
	fmt.Fprintf(&g.out, "define void @%s(ptr sret(%%slick.out) %%ret, ptr %%ctx, ptr %%args) {\nentry:\n%s}\n\n", sym, g.fn.body.String())
	g.fn = saved
	return g.callVal("slick_rt_callable", fmt.Sprintf("ptr @%s, i32 %d, ptr %s, i32 %d", sym, len(caps), g.packVals(caps), len(node.params))), nil
}

func (g *llvmGen) emitCall(node *callExpression, scope *llvmScope) (string, string, error) {
	if node.resolvedCallable {
		return g.emitCallableCall(node, scope)
	}
	name, ok := node.callee.(*nameExpression)
	if !ok {
		return "", "", fmt.Errorf("generated call target is not a name")
	}
	if g.program.usesContext {
		cc, cv := g.callOut("slick_rt_check_cancel", "ptr %ctx")
		if err := g.failIf(cc, cv); err != nil {
			return "", "", err
		}
	}
	args := make([]string, 0, len(node.args))
	argT := make([]string, 0, len(node.args))
	for _, a := range node.args {
		v, c, err := g.emitExpr(a, scope)
		if err != nil {
			return "", "", err
		}
		if err := g.failIf(c, v); err != nil {
			return "", "", err
		}
		t, err := g.exprType(a, scope)
		if err != nil {
			return "", "", err
		}
		args = append(args, v)
		argT = append(argT, t)
	}
	if handled, v, c, err := g.emitArrayCall(node, name, scope, args, argT); handled {
		return v, c, err
	}
	if handled, v, c, err := g.emitMapCall(name, scope, args, argT); handled {
		return v, c, err
	}
	if handled, v, c, err := g.emitBufferCall(node, args, argT); handled {
		return v, c, err
	}
	if name.name == "unwrap" {
		okp := g.reg()
		g.emit("  %s = alloca %%slick.val, align 8", okp)
		g.emit("  store %%slick.val %s, ptr %s, align 8", args[0], okp)
		okf := g.reg()
		g.emit("  %s = call i32 @slick_rt_result_ok_p(ptr %s)", okf, okp)
		isok := g.reg()
		g.emit("  %s = icmp ne i32 %s, 0", isok, okf)
		yes, no := g.label("unwoky"), g.label("unwokn")
		g.emit("  br i1 %s, label %%%s, label %%%s", isok, yes, no)
		g.emit("%s:", no)
		g.retOut(strconv.Itoa(slickCodeThrow), g.ptrCall("slick_rt_result_payload_p", []string{args[0]}, ""))
		g.emit("%s:", yes)
		return g.ptrCall("slick_rt_result_payload_p", []string{args[0]}, ""), strconv.Itoa(slickCodeOK), nil
	}
	if name.name == "enumerate" {
		return g.ptrCall("slick_rt_iter_enum_p", []string{args[0]}, ""), strconv.Itoa(slickCodeOK), nil
	}

	if name.name == "zip" {
		return g.callVal("slick_rt_iter_zip", fmt.Sprintf("i64 %d, ptr %s", len(args), g.packVals(args))), strconv.Itoa(slickCodeOK), nil
	}
	if _, shadowed := scope.locals[strings.Split(name.name, ".")[0]]; !shadowed {
		if union, variant, named := g.program.resolveVariant(scope.function.namespace, scope.function.aliases, name.name); named && variant != nil {
			id := g.unionID[union.qualified]
			fts := g.program.variantFieldTypes(union, variant)
			for i := range args {
				if i < len(fts) {
					args[i] = g.convert(args[i], argT[i], fts[i])
				}
			}
			return g.callVal("slick_rt_union", fmt.Sprintf("i32 %d, i32 %d, i32 %d, ptr %s", id, variant.tag, len(args), g.packVals(args))), strconv.Itoa(slickCodeOK), nil
		}
	}
	if errorType, isError := g.program.resolveErrorIn(scope.function.namespace, scope.function.aliases, name.name); isError && g.program.classes[errorType] != nil {
		msg := g.callVal("slick_rt_string", fmt.Sprintf("ptr %s, i64 0", g.intern("")))
		if len(args) > 0 {
			msg = g.callVal("slick_rt_format", "%slick.val "+args[0])
		}
		id := g.typeID[errorType]
		fields := g.program.classes[errorType].fields
		vals := make([]string, len(g.fieldIdx[errorType]))
		for name, i := range g.fieldIdx[errorType] {
			if name == "Message" {
				vals[i] = msg
			} else {
				vals[i] = g.null()
			}
		}
		_ = fields
		return g.callVal("slick_rt_class", fmt.Sprintf("i32 %d, i32 %d, ptr %s", id, len(vals), g.packVals(vals))), strconv.Itoa(slickCodeOK), nil
	}
	if node.resolvedNative == nativeStdJsonDecode || node.resolvedNative == nativeStdJsonEncode {
		if len(node.resolvedTypeArgs) != 1 {
			return "", "", fmt.Errorf("LLVM JSON lowering requires one concrete type argument")
		}
		helper := "slick_nat_json_decode"
		if node.resolvedNative == nativeStdJsonEncode {
			helper = "slick_nat_json_encode"
		}
		code, value := g.callOut(helper, fmt.Sprintf("ptr %%ctx, ptr %s, ptr %s", g.packVals(args), g.intern(g.jsonSchema(node.resolvedTypeArgs[0]))))
		return value, code, nil
	}
	parts := strings.Split(name.name, ".")
	var target string
	var params []string
	if len(parts) == 2 {
		if recv, exists := scope.locals[parts[0]]; exists {
			recvVal := g.loadBind(recv)
			if iface := g.program.interfaces[recv.typ]; iface != nil {
				slot := indexOf(g.ifaceMethods[recv.typ], parts[1])
				code, val := g.callOut("slick_rt_iface_call", fmt.Sprintf("ptr %%ctx, %%slick.val %s, i32 %d, i32 %d, ptr %s", recvVal, slot, len(args), g.packVals(args)))
				return val, code, nil
			}
			target = llvmMethodSymbol(recv.typ, parts[1])
			method, found := g.program.methodForType(recv.typ, parts[1])
			if !found {
				return "", "", fmt.Errorf("unknown generated method %s", name.name)
			}
			args = append([]string{recvVal}, args...)
			for _, p := range method.params {
				params = append(params, g.program.resolveType(method.namespace, method.aliases, p.typ))
			}
			for i := 1; i < len(args) && i-1 < len(params); i++ {
				args[i] = g.convert(args[i], argT[i-1], params[i-1])
			}
			code, val := g.callOut(target, fmt.Sprintf("ptr %%ctx, ptr %s", g.packVals(args)))
			return val, code, nil
		}
	}
	fn := g.program.callTarget(scope.function, node, name.name)
	if fn == nil {
		return "", "", fmt.Errorf("unknown generated function %s", name.name)
	}
	target = llvmFunctionSymbol(fn.qualified)
	for i := range args {
		if i < len(fn.params) {
			declared := g.program.resolveType(fn.namespace, fn.aliases, fn.params[i].typ)
			args[i] = g.convert(args[i], argT[i], declared)
		}
	}
	code, val := g.callOut(target, fmt.Sprintf("ptr %%ctx, ptr %s", g.packVals(args)))
	return val, code, nil
}

func indexOf(xs []string, v string) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return 0
}

func (g *llvmGen) emitCallableCall(node *callExpression, scope *llvmScope) (string, string, error) {
	if g.program.usesContext {
		cc, cv := g.callOut("slick_rt_check_cancel", "ptr %ctx")
		if err := g.failIf(cc, cv); err != nil {
			return "", "", err
		}
	}
	callee, c, err := g.emitExpr(node.callee, scope)
	if err != nil {
		return "", "", err
	}
	if err := g.failIf(c, callee); err != nil {
		return "", "", err
	}
	args := make([]string, 0, len(node.args))
	for i, a := range node.args {
		v, c, err := g.emitExpr(a, scope)
		if err != nil {
			return "", "", err
		}
		if err := g.failIf(c, v); err != nil {
			return "", "", err
		}
		if i < len(node.resolvedParams) {
			from, _ := g.exprType(a, scope)
			v = g.convert(v, from, node.resolvedParams[i])
		}
		args = append(args, v)
	}
	code, val := g.callOut("slick_rt_invoke", fmt.Sprintf("ptr %%ctx, %%slick.val %s, i32 %d, ptr %s", callee, len(args), g.packVals(args)))
	return val, code, nil
}

func (g *llvmGen) emitArrayCall(node *callExpression, name *nameExpression, scope *llvmScope, args, argT []string) (bool, string, string, error) {
	parts := strings.Split(name.name, ".")
	if len(parts) != 2 {
		return false, "", "", nil
	}
	recv, exists := scope.locals[parts[0]]
	if !exists {
		return false, "", "", nil
	}
	elem, isArray := arrayElementType(recv.typ)
	if !isArray {
		return false, "", "", nil
	}
	recvVal := g.loadBind(recv)
	switch parts[1] {
	case "Length":
		n := g.reg()
		np := g.reg()
		g.emit("  %s = alloca %%slick.val, align 8", np)
		g.emit("  store %%slick.val %s, ptr %s, align 8", recvVal, np)
		g.emit("  %s = call i64 @slick_rt_array_len_p(ptr %s)", n, np)
		return true, g.callVal("slick_rt_int", "i64 "+n), strconv.Itoa(slickCodeOK), nil
	case "Get":
		idx := g.reg()
		g.emit("  %s = extractvalue %%slick.val %s, 1", idx, args[0])
		return true, g.ptrCall("slick_rt_array_get_p", []string{recvVal}, "i64 "+idx), strconv.Itoa(slickCodeOK), nil
	case "Slice":
		s, e := g.reg(), g.reg()
		g.emit("  %s = extractvalue %%slick.val %s, 1", s, args[0])
		g.emit("  %s = extractvalue %%slick.val %s, 1", e, args[1])
		fail := g.typeID[stdCollectionsBoundsFailureName]
		return true, g.callVal("slick_rt_array_slice", fmt.Sprintf("%%slick.val %s, i64 %s, i64 %s, i32 %d", recvVal, s, e, fail)), strconv.Itoa(slickCodeOK), nil
	}
	_ = elem
	_ = argT
	return false, "", "", nil
}

func (g *llvmGen) emitMapCall(name *nameExpression, scope *llvmScope, args, argT []string) (bool, string, string, error) {
	parts := strings.Split(name.name, ".")
	if len(parts) != 2 {
		return false, "", "", nil
	}
	recv, exists := scope.locals[parts[0]]
	if !exists {
		return false, "", "", nil
	}
	keyT, valT, isMap := mapTypeArgs(recv.typ)
	if !isMap {
		return false, "", "", nil
	}
	recvVal := g.loadBind(recv)
	switch parts[1] {
	case "Get":
		return true, g.ptrCall("slick_rt_map_get_p", []string{recvVal, g.convert(args[0], argT[0], keyT)}, ""), strconv.Itoa(slickCodeOK), nil
	case "Contains":
		n := g.reg()
		ap, bp := g.reg(), g.reg()
		g.emit("  %s = alloca %%slick.val, align 8", ap)
		g.emit("  store %%slick.val %s, ptr %s, align 8", recvVal, ap)
		g.emit("  %s = alloca %%slick.val, align 8", bp)
		g.emit("  store %%slick.val %s, ptr %s, align 8", g.convert(args[0], argT[0], keyT), bp)
		g.emit("  %s = call i32 @slick_rt_map_contains_p(ptr %s, ptr %s)", n, ap, bp)
		return true, g.callVal("slick_rt_bool", "i32 "+n), strconv.Itoa(slickCodeOK), nil
	case "With":
		return true, g.ptrCall("slick_rt_map_with_p", []string{recvVal, g.convert(args[0], argT[0], keyT), g.convert(args[1], argT[1], valT)}, ""), strconv.Itoa(slickCodeOK), nil
	case "Without":
		return true, g.ptrCall("slick_rt_map_without_p", []string{recvVal, g.convert(args[0], argT[0], keyT)}, ""), strconv.Itoa(slickCodeOK), nil
	case "Length":
		n := g.reg()
		np := g.reg()
		g.emit("  %s = alloca %%slick.val, align 8", np)
		g.emit("  store %%slick.val %s, ptr %s, align 8", recvVal, np)
		g.emit("  %s = call i64 @slick_rt_map_len_p(ptr %s)", n, np)
		return true, g.callVal("slick_rt_int", "i64 "+n), strconv.Itoa(slickCodeOK), nil
	}

	return false, "", "", nil
}

func (g *llvmGen) emitBufferCall(node *callExpression, args, argT []string) (bool, string, string, error) {
	if !isNativeStdBuffer(node.resolvedNative) {
		return false, "", "", nil
	}
	for i := range args {
		if i < len(node.resolvedParams) {
			args[i] = g.convert(args[i], argT[i], node.resolvedParams[i])
		}
	}
	switch node.resolvedNative {
	case nativeStdBufferNew:
		return true, g.callVal("slick_rt_buffer_new", ""), strconv.Itoa(slickCodeOK), nil
	case nativeStdBufferPush:
		g.emit("  call void @slick_rt_buffer_push(%%slick.val %s, %%slick.val %s)", args[0], args[1])
		return true, g.null(), strconv.Itoa(slickCodeOK), nil
	case nativeStdBufferGet:
		idx := g.reg()
		g.emit("  %s = extractvalue %%slick.val %s, 1", idx, args[1])
		return true, g.callVal("slick_rt_buffer_get", fmt.Sprintf("%%slick.val %s, i64 %s", args[0], idx)), strconv.Itoa(slickCodeOK), nil
	case nativeStdBufferSet:
		idx := g.reg()
		g.emit("  %s = extractvalue %%slick.val %s, 1", idx, args[1])
		fail := g.typeID[stdCollectionsBoundsFailureName]
		return true, g.callVal("slick_rt_buffer_set", fmt.Sprintf("%%slick.val %s, i64 %s, %%slick.val %s, i32 %d", args[0], idx, args[2], fail)), strconv.Itoa(slickCodeOK), nil
	case nativeStdBufferLength:
		n := g.reg()
		g.emit("  %s = call i64 @slick_rt_buffer_len(%%slick.val %s)", n, args[0])
		return true, g.callVal("slick_rt_int", "i64 "+n), strconv.Itoa(slickCodeOK), nil
	case nativeStdBufferFreeze:
		return true, g.callVal("slick_rt_buffer_freeze", "%slick.val "+args[0]), strconv.Itoa(slickCodeOK), nil
	}
	return false, "", "", nil
}

func (g *llvmGen) emitBinary(node *binaryExpression, scope *llvmScope) (string, string, error) {
	left, c, err := g.emitExpr(node.left, scope)
	if err != nil {
		return "", "", err
	}
	if err := g.failIf(c, left); err != nil {
		return "", "", err
	}
	if node.op == "&&" || node.op == "||" {
		t := g.reg()
		g.emit("  %s = call i32 @slick_rt_truth(%%slick.val %s)", t, left)
		nz := g.reg()
		g.emit("  %s = icmp ne i32 %s, 0", nz, t)
		yes, no, done := g.label("scyes"), g.label("scno"), g.label("scdone")
		if node.op == "&&" {
			g.emit("  br i1 %s, label %%%s, label %%%s", nz, yes, no)
			g.emit("%s:", no)
			g.emit("  br label %%%s", done)
			g.emit("%s:", yes)
			right, c, err := g.emitExpr(node.right, scope)
			if err != nil {
				return "", "", err
			}
			fromYes := g.fn.cur
			g.emit("  br label %%%s", done)
			g.emit("%s:", done)
			phi, code := g.reg(), g.reg()
			g.emit("  %s = phi %%slick.val [ %s, %%%s ], [ %s, %%%s ]", phi, left, no, right, fromYes)
			g.emit("  %s = phi i32 [ 0, %%%s ], [ %s, %%%s ]", code, no, c, fromYes)
			return phi, code, nil
		}
		g.emit("  br i1 %s, label %%%s, label %%%s", nz, yes, no)
		g.emit("%s:", yes)
		g.emit("  br label %%%s", done)
		g.emit("%s:", no)
		right, c, err := g.emitExpr(node.right, scope)
		if err != nil {
			return "", "", err
		}
		fromNo := g.fn.cur
		g.emit("  br label %%%s", done)
		g.emit("%s:", done)
		phi, code := g.reg(), g.reg()
		g.emit("  %s = phi %%slick.val [ %s, %%%s ], [ %s, %%%s ]", phi, left, yes, right, fromNo)
		g.emit("  %s = phi i32 [ 0, %%%s ], [ %s, %%%s ]", code, yes, c, fromNo)
		return phi, code, nil
	}
	right, c, err := g.emitExpr(node.right, scope)
	if err != nil {
		return "", "", err
	}
	if err := g.failIf(c, right); err != nil {
		return "", "", err
	}
	switch node.op {
	case "+":
		return g.ptrCall("slick_rt_add_p", []string{left, right}, ""), strconv.Itoa(slickCodeOK), nil
	case "-":
		return g.ptrCall("slick_rt_sub_p", []string{left, right}, ""), strconv.Itoa(slickCodeOK), nil
	case "*":
		return g.ptrCall("slick_rt_mul_p", []string{left, right}, ""), strconv.Itoa(slickCodeOK), nil
	case "<", "<=", ">", ">=":
		op := map[string]int{"<": 0, "<=": 1, ">": 2, ">=": 3}[node.op]
		return g.ptrCall("slick_rt_cmp_p", []string{left, right}, fmt.Sprintf("i32 %d", op)), strconv.Itoa(slickCodeOK), nil
	default:
		lt, _ := g.exprType(node.left, scope)
		rt, _ := g.exprType(node.right, scope)
		if joined, ok := joinTypes(lt, rt); ok {
			left = g.convert(left, lt, joined)
			right = g.convert(right, rt, joined)
		}
		ap, bp := g.reg(), g.reg()
		g.emit("  %s = alloca %%slick.val, align 8", ap)
		g.emit("  store %%slick.val %s, ptr %s, align 8", left, ap)
		g.emit("  %s = alloca %%slick.val, align 8", bp)
		g.emit("  store %%slick.val %s, ptr %s, align 8", right, bp)
		eq := g.reg()
		g.emit("  %s = call i32 @slick_rt_equal_p(ptr %s, ptr %s)", eq, ap, bp)
		if node.op == "!=" {
			fl := g.reg()
			g.emit("  %s = xor i32 %s, 1", fl, eq)
			eq = fl
		}
		return g.callVal("slick_rt_bool", "i32 "+eq), strconv.Itoa(slickCodeOK), nil
	}
}

func (g *llvmGen) emitIf(node *ifExpression, scope *llvmScope) (string, string, error) {
	cond, c, err := g.emitExpr(node.condition, scope)
	if err != nil {
		return "", "", err
	}
	if err := g.failIf(c, cond); err != nil {
		return "", "", err
	}
	tp := g.reg()
	g.emit("  %s = alloca %%slick.val, align 8", tp)
	g.emit("  store %%slick.val %s, ptr %s, align 8", cond, tp)
	t := g.reg()
	g.emit("  %s = call i32 @slick_rt_truth_p(ptr %s)", t, tp)
	nz := g.reg()
	g.emit("  %s = icmp ne i32 %s, 0", nz, t)
	yes, no, done := g.label("ify"), g.label("ifn"), g.label("ifd")
	slot := g.reg()
	codeSlot := g.reg()
	g.emit("  %s = alloca %%slick.val, align 8", slot)
	g.emit("  %s = alloca i32, align 4", codeSlot)
	g.emit("  store i32 0, ptr %s, align 4", codeSlot)
	g.emit("  br i1 %s, label %%%s, label %%%s", nz, yes, no)
	typ, err := g.exprType(node, scope)
	if err != nil {
		return "", "", err
	}
	g.emit("%s:", yes)
	thenScope := scope.clone()
	tv, tc, err := g.emitBlock(node.thenBlock, thenScope, typ, g.narrow(node.condition, scope, thenScope, true))
	if err != nil {
		return "", "", err
	}
	g.emit("  store %%slick.val %s, ptr %s, align 8", tv, slot)
	g.emit("  store i32 %s, ptr %s, align 4", tc, codeSlot)
	g.emit("  br label %%%s", done)
	g.emit("%s:", no)
	if node.elseBlock != nil {
		elseScope := scope.clone()
		ev, ec, err := g.emitBlock(node.elseBlock, elseScope, typ, g.narrow(node.condition, scope, elseScope, false))
		if err != nil {
			return "", "", err
		}
		g.emit("  store %%slick.val %s, ptr %s, align 8", ev, slot)
		g.emit("  store i32 %s, ptr %s, align 4", ec, codeSlot)
	} else {
		g.emit("  store %%slick.val %s, ptr %s, align 8", g.null(), slot)
	}
	g.emit("  br label %%%s", done)
	g.emit("%s:", done)
	out := g.reg()
	outC := g.reg()
	g.emit("  %s = load %%slick.val, ptr %s, align 8", out, slot)
	g.emit("  %s = load i32, ptr %s, align 4", outC, codeSlot)
	return out, outC, nil
}

func (g *llvmGen) narrow(cond expressionNode, outer, branch *llvmScope, then bool) string {
	name, present, ok := nullTestOf(cond)
	if !ok || present != then {
		return ""
	}
	bind, exists := outer.locals[name]
	if !exists {
		return ""
	}
	base, optional := optionalBase(bind.typ)
	if !optional {
		return ""
	}
	v := g.ptrCall("slick_rt_optional_value_p", []string{g.loadBind(bind)}, "")
	g.setLocal(branch, name, base, v)
	return ""
}

func (g *llvmGen) emitCatch(node *catchExpression, scope *llvmScope) (string, string, error) {
	v, c, err := g.emitExpr(node.value, scope)
	if err != nil {
		return "", "", err
	}
	end := g.label("cate")
	slot := g.reg()
	g.emit("  %s = alloca %%slick.val, align 8", slot)
	isThrow := g.reg()
	g.emit("  %s = icmp eq i32 %s, %d", isThrow, c, slickCodeThrow)
	yes, no := g.label("caty"), g.label("catn")
	g.emit("  br i1 %s, label %%%s, label %%%s", isThrow, yes, no)
	g.emit("%s:", no)
	ctrl := g.reg()
	g.emit("  %s = call i32 @slick_rt_is_control(i32 %s)", ctrl, c)
	cnz := g.reg()
	g.emit("  %s = icmp ne i32 %s, 0", cnz, ctrl)
	cf, cok := g.label("catc"), g.label("cato")
	g.emit("  br i1 %s, label %%%s, label %%%s", cnz, cf, cok)
	g.emit("%s:", cf)
	g.retOut(c, v)
	g.emit("%s:", cok)
	g.emit("  store %%slick.val %s, ptr %s, align 8", v, slot)
	g.emit("  br label %%%s", end)
	g.emit("%s:", yes)
	for _, arm := range node.arms {
		errorType, ok := g.program.resolveErrorIn(scope.function.namespace, scope.function.aliases, arm.errorType.name)
		if !ok {
			continue
		}
		armScope := scope.clone()
		binding := arm.binding
		if binding == "" {
			binding = node.binding
		}
		if errorType == "Error" {
			if binding != "" {
				g.setLocal(armScope, binding, "Error", v)
			}
			av, ac, err := g.emitExpr(arm.value, armScope)
			if err != nil {
				return "", "", err
			}
			g.failIf(ac, av)
			g.emit("  store %%slick.val %s, ptr %s, align 8", av, slot)
			g.emit("  br label %%%s", end)
			g.emit("%s:", end)
			out := g.reg()
			g.emit("  %s = load %%slick.val, ptr %s, align 8", out, slot)
			return out, strconv.Itoa(slickCodeOK), nil
		}
		tid := g.reg()
		value := g.reg()
		g.emit("  %s = alloca %%slick.val, align 8", value)
		g.emit("  store %%slick.val %s, ptr %s, align 8", v, value)
		g.emit("  %s = call i32 @slick_rt_class_type_p(ptr %s)", tid, value)
		eq := g.reg()
		g.emit("  %s = icmp eq i32 %s, %d", eq, tid, g.typeID[errorType])
		hit, miss := g.label("cath"), g.label("catm")
		g.emit("  br i1 %s, label %%%s, label %%%s", eq, hit, miss)
		g.emit("%s:", hit)
		if binding != "" {
			g.setLocal(armScope, binding, errorType, v)
		}
		av, ac, err := g.emitExpr(arm.value, armScope)
		if err != nil {
			return "", "", err
		}
		g.failIf(ac, av)
		g.emit("  store %%slick.val %s, ptr %s, align 8", av, slot)
		g.emit("  br label %%%s", end)
		g.emit("%s:", miss)
	}
	g.retOut(c, v)
	g.emit("%s:", end)
	out := g.reg()
	g.emit("  %s = load %%slick.val, ptr %s, align 8", out, slot)
	return out, strconv.Itoa(slickCodeOK), nil
}

func (g *llvmGen) emitResult(node *resultExpression, scope *llvmScope) (string, string, error) {
	typ, err := g.exprType(node, scope)
	if err != nil {
		return "", "", err
	}
	payload, c, err := g.emitExpr(node.value, scope)
	if err != nil {
		return "", "", err
	}
	if err := g.failIf(c, payload); err != nil {
		return "", "", err
	}
	from, _ := g.exprType(node.value, scope)
	okn, fail, _ := resultTypeArgs(typ)
	target := okn
	flag := 1
	if !node.ok {
		target = fail
		flag = 0
	}
	return g.ptrCall("slick_rt_result_p", []string{g.convert(payload, from, target)}, fmt.Sprintf("i32 %d", flag)), strconv.Itoa(slickCodeOK), nil
}

func (g *llvmGen) emitPropagate(node *propagateExpression, scope *llvmScope) (string, string, error) {
	v, c, err := g.emitExpr(node.value, scope)
	if err != nil {
		return "", "", err
	}
	if err := g.failIf(c, v); err != nil {
		return "", "", err
	}
	okp := g.reg()
	g.emit("  %s = alloca %%slick.val, align 8", okp)
	g.emit("  store %%slick.val %s, ptr %s, align 8", v, okp)
	okf := g.reg()
	g.emit("  %s = call i32 @slick_rt_result_ok_p(ptr %s)", okf, okp)
	isok := g.reg()
	g.emit("  %s = icmp ne i32 %s, 0", isok, okf)
	yes, no, done := g.label("pry"), g.label("prn"), g.label("prd")
	g.emit("  br i1 %s, label %%%s, label %%%s", isok, yes, no)
	g.emit("%s:", no)
	fail := g.ptrCall("slick_rt_result_payload_p", []string{v}, "")
	wrapped := g.ptrCall("slick_rt_result_p", []string{fail}, "i32 0")
	g.emit("  br label %%%s", done)
	g.emit("%s:", yes)
	payload := g.ptrCall("slick_rt_result_payload_p", []string{v}, "")
	g.emit("  br label %%%s", done)
	g.emit("%s:", done)
	value, code := g.reg(), g.reg()
	g.emit("  %s = phi %%slick.val [ %s, %%%s ], [ %s, %%%s ]", value, wrapped, no, payload, yes)
	g.emit("  %s = phi i32 [ %d, %%%s ], [ %d, %%%s ]", code, slickCodeReturn, no, slickCodeOK, yes)
	return value, code, nil
}

func (g *llvmGen) emitUsing(node *usingExpression, scope *llvmScope) (string, string, error) {
	res, c, err := g.emitExpr(node.initializer, scope)
	if err != nil {
		return "", "", err
	}
	if err := g.failIf(c, res); err != nil {
		return "", "", err
	}
	us := scope.clone()
	g.setLocal(us, node.name, node.resolved, res)
	bodyV, bodyC, err := g.emitBlock(node.body, us, node.result, "")
	if err != nil {
		return "", "", err
	}
	cleanupCtx := g.reg()
	g.emit("  %s = call ptr @slick_rt_cleanup_ctx()", cleanupCtx)
	var cc, cv string
	if g.program.interfaces[node.resolved] != nil {
		slot := indexOf(g.ifaceMethods[node.resolved], "Close")
		cc, cv = g.callOut("slick_rt_iface_call", fmt.Sprintf("ptr %s, %%slick.val %s, i32 %d, i32 0, ptr null", cleanupCtx, res, slot))
	} else {
		closeName := llvmMethodSymbol(node.resolved, "Close")
		cc, cv = g.callOut(closeName, fmt.Sprintf("ptr %s, ptr %s", cleanupCtx, g.packVals([]string{res})))
	}
	valueSlot, codeSlot := g.reg(), g.reg()
	g.emit("  %s = alloca %%slick.val, align 8", valueSlot)
	g.emit("  %s = alloca i32, align 4", codeSlot)
	isClose := g.reg()
	g.emit("  %s = icmp ne i32 %s, 0", isClose, cc)
	cfail, cok, done := g.label("uscf"), g.label("uscok"), g.label("usd")
	g.emit("  br i1 %s, label %%%s, label %%%s", isClose, cfail, cok)
	g.emit("%s:", cok)
	g.emit("  store %%slick.val %s, ptr %s, align 8", bodyV, valueSlot)
	g.emit("  store i32 %s, ptr %s, align 4", bodyC, codeSlot)
	g.emit("  br label %%%s", done)
	g.emit("%s:", cfail)
	bodyOK := g.reg()
	g.emit("  %s = icmp eq i32 %s, 0", bodyOK, bodyC)
	ctrl := g.reg()
	g.emit("  %s = call i32 @slick_rt_is_control(i32 %s)", ctrl, bodyC)
	ctrlNZ := g.reg()
	g.emit("  %s = icmp ne i32 %s, 0", ctrlNZ, ctrl)
	preferClose := g.reg()
	g.emit("  %s = or i1 %s, %s", preferClose, bodyOK, ctrlNZ)
	pref, sup := g.label("uspref"), g.label("ussup")
	g.emit("  br i1 %s, label %%%s, label %%%s", preferClose, pref, sup)
	g.emit("%s:", pref)
	g.emit("  store %%slick.val %s, ptr %s, align 8", cv, valueSlot)
	g.emit("  store i32 %s, ptr %s, align 4", cc, codeSlot)
	g.emit("  br label %%%s", done)
	g.emit("%s:", sup)
	combined := g.ptrCall("slick_rt_suppress_p", []string{bodyV, cv}, "")
	g.emit("  store %%slick.val %s, ptr %s, align 8", combined, valueSlot)
	g.emit("  store i32 %s, ptr %s, align 4", bodyC, codeSlot)
	g.emit("  br label %%%s", done)
	g.emit("%s:", done)
	value, code := g.reg(), g.reg()
	g.emit("  %s = load %%slick.val, ptr %s, align 8", value, valueSlot)
	g.emit("  %s = load i32, ptr %s, align 4", code, codeSlot)
	return value, code, nil
}

func (g *llvmGen) emitMatch(node *matchExpression, scope *llvmScope) (string, string, error) {
	operand, err := g.exprType(node.value, scope)
	if err != nil {
		return "", "", err
	}
	scrut, c, err := g.emitExpr(node.value, scope)
	if err != nil {
		return "", "", err
	}
	if err := g.failIf(c, scrut); err != nil {
		return "", "", err
	}
	end := g.label("me")
	slot := g.reg()
	g.emit("  %s = alloca %%slick.val, align 8", slot)
	g.emit("  store %%slick.val %s, ptr %s, align 8", g.null(), slot)
	if union := g.program.unions[operand]; union != nil {
		tag := g.reg()
		g.emit("  %s = call i32 @slick_rt_union_tag(%%slick.val %s)", tag, scrut)
		for _, arm := range node.arms {
			armScope := scope.clone()
			if arm.pattern == matchPatternVariant {
				variant := union.variants[arm.resolvedVariant]
				eq := g.reg()
				g.emit("  %s = icmp eq i32 %s, %d", eq, tag, variant.tag)
				hit, miss := g.label("mh"), g.label("mm")
				g.emit("  br i1 %s, label %%%s, label %%%s", eq, hit, miss)
				g.emit("%s:", hit)
				for i, b := range arm.bindings {
					if b == "_" || i >= len(variant.fields) {
						continue
					}
					declared := g.program.resolveType(union.namespace, union.aliases, variant.fields[i].typ)
					val := g.ptrCall("slick_rt_union_field_p", []string{scrut}, fmt.Sprintf("i32 %d", i))
					g.setLocal(armScope, b, declared, val)
				}
				av, ac, err := g.emitExpr(arm.value, armScope)
				if err != nil {
					return "", "", err
				}
				g.failIf(ac, av)
				g.emit("  store %%slick.val %s, ptr %s, align 8", av, slot)
				g.emit("  br label %%%s", end)
				g.emit("%s:", miss)
				continue
			}
			av, ac, err := g.emitExpr(arm.value, armScope)
			if err != nil {
				return "", "", err
			}
			g.failIf(ac, av)
			g.emit("  store %%slick.val %s, ptr %s, align 8", av, slot)
			g.emit("  br label %%%s", end)
		}
		g.emit("  br label %%%s", end)
		g.emit("%s:", end)
		out := g.reg()
		g.emit("  %s = load %%slick.val, ptr %s, align 8", out, slot)
		return out, strconv.Itoa(slickCodeOK), nil
	}
	okp := g.reg()
	g.emit("  %s = alloca %%slick.val, align 8", okp)
	g.emit("  store %%slick.val %s, ptr %s, align 8", scrut, okp)
	okf := g.reg()
	g.emit("  %s = call i32 @slick_rt_result_ok_p(ptr %s)", okf, okp)
	success, failure, _ := resultTypeArgs(operand)
	for _, arm := range node.arms {
		armScope := scope.clone()
		switch arm.pattern {
		case matchPatternOk, matchPatternErr:
			want := 1
			bindT := success
			if arm.pattern == matchPatternErr {
				want = 0
				bindT = failure
			}
			eq := g.reg()
			g.emit("  %s = icmp eq i32 %s, %d", eq, okf, want)
			hit, miss := g.label("rh"), g.label("rm")
			g.emit("  br i1 %s, label %%%s, label %%%s", eq, hit, miss)
			g.emit("%s:", hit)
			if arm.binding != "" {
				val := g.ptrCall("slick_rt_result_payload_p", []string{scrut}, "")
				g.setLocal(armScope, arm.binding, bindT, val)
			}
			av, ac, err := g.emitExpr(arm.value, armScope)
			if err != nil {
				return "", "", err
			}
			g.failIf(ac, av)
			g.emit("  store %%slick.val %s, ptr %s, align 8", av, slot)
			g.emit("  br label %%%s", end)
			g.emit("%s:", miss)
		default:
			av, ac, err := g.emitExpr(arm.value, armScope)
			if err != nil {
				return "", "", err
			}
			g.failIf(ac, av)
			g.emit("  store %%slick.val %s, ptr %s, align 8", av, slot)
			g.emit("  br label %%%s", end)
		}
	}
	g.emit("  br label %%%s", end)
	g.emit("%s:", end)
	out := g.reg()
	g.emit("  %s = load %%slick.val, ptr %s, align 8", out, slot)
	return out, strconv.Itoa(slickCodeOK), nil
}

func (g *llvmGen) template(text string, scope *llvmScope) (string, error) {
	var acc string
	first := true
	for {
		start := strings.Index(text, "${")
		if start < 0 {
			lit := g.literal(text)
			if first {
				return lit, nil
			}
			return g.ptrCall("slick_rt_add_p", []string{acc, lit}, ""), nil
		}
		if start > 0 {
			lit := g.literal(text[:start])
			if first {
				acc = lit
				first = false
			} else {
				acc = g.ptrCall("slick_rt_add_p", []string{acc, lit}, "")
			}
		}
		text = text[start+2:]
		end := strings.IndexByte(text, '}')
		if end < 0 {
			return "", fmt.Errorf("unterminated generated template")
		}
		name := strings.TrimSpace(text[:end])
		val, err := g.nameValue(&nameExpression{name: name}, scope)
		if err != nil {
			return "", err
		}
		formatted := g.ptrCall("slick_rt_format_p", []string{val}, "")
		if first {
			acc = formatted
			first = false
		} else {
			acc = g.ptrCall("slick_rt_add_p", []string{acc, formatted}, "")
		}
		text = text[end+1:]
	}
}

func (g *llvmGen) jsonSchema(typ string) string {
	if base, optional := optionalBase(typ); optional {
		return "?" + g.jsonSchema(base)
	}
	if element, array := arrayElementType(typ); array {
		return "[" + g.jsonSchema(element)
	}
	switch typ {
	case "null":
		return "n"
	case "bool":
		return "b"
	case "int":
		return "i"
	case "float":
		return "f"
	case "string":
		return "s"
	}
	if id, ok := g.typeID[typ]; ok {
		return "c" + strconv.Itoa(id) + ";"
	}
	return "x"
}

func (g *llvmGen) convert(value, from, to string) string {
	if from == to || to == "" || from == typeUnknown || from == typeNever {
		return value
	}
	if iface := g.program.interfaces[to]; iface != nil {
		if class := g.program.classes[from]; class != nil {
			return g.wrapIface(value, from, to)
		}
	}
	base, optional := optionalBase(to)
	if !optional {
		return value
	}
	if from == "null" {
		return g.callVal("slick_rt_none", "")
	}
	if isOptionalType(from) {
		return value
	}
	_ = base
	return g.callVal("slick_rt_some", "%slick.val "+value)
}

func (g *llvmGen) ifaceVTable(ifaceName, className string) string {
	key := ifaceName + "\x00" + className
	if _, ok := g.vtables[key]; !ok {
		methods := g.ifaceMethods[ifaceName]
		ptrs := make([]string, len(methods))
		for i, method := range methods {
			ptrs[i] = fmt.Sprintf("ptr @%s", llvmMethodSymbol(className, method))
		}
		name := fmt.Sprintf("@.vt.%d", len(g.vtables))
		g.vtables[key] = name
		fmt.Fprintf(&g.decl, "%s = private global [%d x ptr] [%s]\n", name, max(1, len(methods)), strings.Join(ptrs, ", "))
	}
	return g.vtables[key]
}

func (g *llvmGen) wrapIface(recv, className, ifaceName string) string {
	return g.callVal("slick_rt_iface", fmt.Sprintf("i32 %d, %%slick.val %s, i32 %d, ptr %s", g.typeID[ifaceName], recv, len(g.ifaceMethods[ifaceName]), g.ifaceVTable(ifaceName, className)))
}

func (g *llvmGen) exprType(expr expressionNode, scope *llvmScope) (string, error) {
	switch node := expr.(type) {
	case *tupleExpression:
		if node.resolved != "" {
			return node.resolved, nil
		}
	case *arrayExpression:
		if node.resolved != "" {
			return node.resolved, nil
		}
	case *usingExpression:
		if node.result != "" {
			return node.result, nil
		}
	case *awaitExpression:
		if node.resolved != "" {
			return node.resolved, nil
		}
	case *lambdaExpression:
		if node.resolved != "" {
			return node.resolved, nil
		}
	}
	locals := map[string]string{}
	for n, b := range scope.locals {
		locals[n] = b.typ
	}
	info := g.program.checkASTExpression(expr, &astScope{function: scope.function, locals: locals})
	if info.typ == typeUnknown {
		return "", fmt.Errorf("cannot generate unknown expression type")
	}
	return info.typ, nil
}

func (g *llvmGen) emitMain() error {
	main := g.program.functions["root.main"]
	accepts, err := g.program.mainAcceptsArguments(main)
	if err != nil {
		return err
	}
	result := g.program.resolveType(main.namespace, main.aliases, main.result)
	g.emitTypeInit()
	g.out.WriteString("define i32 @main(i32 %argc, ptr %argv) {\n")
	fmt.Fprintf(&g.out, "  %%abi = load volatile i32, ptr @slick_abi_version_%d\n", NativeABIVersion)
	g.out.WriteString("  call void @slick_init_types()\n")
	g.out.WriteString("  %ctx = call ptr @slick_rt_root_ctx()\n")
	g.out.WriteString("  %slot = alloca [1 x %slick.val]\n")
	if accepts {
		g.out.WriteString("  %has = icmp sgt i32 %argc, 0\n")
		g.out.WriteString("  %ac = select i1 %has, i32 %argc, i32 1\n")
		g.out.WriteString("  %ac1 = sub i32 %ac, 1\n")
		g.out.WriteString("  %av1 = getelementptr ptr, ptr %argv, i32 1\n")
		g.out.WriteString("  %argvv = call %slick.val @slick_rt_argv(i32 %ac1, ptr %av1)\n")
		g.out.WriteString("  %p = getelementptr [1 x %slick.val], ptr %slot, i32 0, i32 0\n")
		g.out.WriteString("  store %slick.val %argvv, ptr %p\n")
	}
	g.out.WriteString("  %outslot = alloca %slick.out\n")
	fmt.Fprintf(&g.out, "  call void @%s(ptr sret(%%slick.out) %%outslot, ptr %%ctx, ptr %%slot)\n", llvmFunctionSymbol(main.qualified))
	g.out.WriteString("  %out = load %slick.out, ptr %outslot\n")
	g.out.WriteString("  %code = extractvalue %slick.out %out, 0\n")
	g.out.WriteString("  %val = extractvalue %slick.out %out, 1\n")
	g.out.WriteString("  %bad = icmp ne i64 %code, 0\n")
	g.out.WriteString("  br i1 %bad, label %fail, label %ok\nfail:\n")
	g.out.WriteString("  %badval = alloca %slick.val\n")
	g.out.WriteString("  store %slick.val %val, ptr %badval\n")
	g.out.WriteString("  %msgval = alloca %slick.val\n")
	g.out.WriteString("  call void @slick_rt_format_p(ptr %msgval, ptr %badval)\n")
	g.out.WriteString("  call void @slick_rt_write_bytes_p(ptr %msgval, i32 2)\n")
	g.out.WriteString("  ret i32 1\nok:\n")
	if result == stdProcessStatusName {
		g.out.WriteString(g.writeStatus())
		g.out.WriteString("}\n")
		return nil
	}
	if result != "null" {
		g.out.WriteString("  %pval = alloca %slick.val\n  store %slick.val %val, ptr %pval\n  call void @slick_rt_print_p(ptr %pval)\n")
	}
	g.out.WriteString("  ret i32 0\n}\n")
	return nil
}

func (g *llvmGen) writeStatus() string {
	var b strings.Builder
	idx := g.fieldIdx[stdProcessStatusName]
	fmt.Fprintf(&b, "  %%outb = call %%slick.val @slick_rt_field(%%slick.val %%val, i32 %d)\n", idx["Output"])
	fmt.Fprintf(&b, "  %%errb = call %%slick.val @slick_rt_field(%%slick.val %%val, i32 %d)\n", idx["ErrorOutput"])
	fmt.Fprintf(&b, "  %%codev = call %%slick.val @slick_rt_field(%%slick.val %%val, i32 %d)\n", idx["ExitCode"])
	b.WriteString("  call void @slick_rt_write_bytes(%slick.val %outb, i32 1)\n")
	b.WriteString("  call void @slick_rt_write_bytes(%slick.val %errb, i32 2)\n")
	b.WriteString("  %exit = extractvalue %slick.val %codev, 1\n")
	b.WriteString("  %exit32 = trunc i64 %exit to i32\n")
	b.WriteString("  ret i32 %exit32\n")
	return b.String()
}

func (g *llvmGen) emitTypeInit() {
	g.out.WriteString("define void @slick_init_types() {\n")
	fmt.Fprintf(&g.out, "  call void @slick_rt_set_type_count(i32 %d)\n", len(g.typeNames))
	for i, name := range g.typeNames {
		class := g.program.classes[name]
		fields := []string{}
		isError, native := 0, 0
		if class != nil {
			fields = sortedKeys(class.fields)
			if class.isError {
				isError = 1
			}
			if class.nativeResource != "" {
				native = 1
			}
		} else if iface := g.program.interfaces[name]; iface != nil {
			fields = g.ifaceMethods[name]
		}
		names, wires, schemas := "null", "null", "null"
		if len(fields) > 0 {
			namePtrs := make([]string, len(fields))
			wirePtrs := make([]string, len(fields))
			schemaPtrs := make([]string, len(fields))
			for j, fieldName := range fields {
				wire, schema := fieldName, "x"
				if class != nil {
					field := class.fields[fieldName]
					wire = field.jsonWireName()
					schema = g.jsonSchema(g.program.resolveType(class.namespace, class.aliases, field.typ))
				}
				namePtrs[j] = fmt.Sprintf("ptr %s", g.intern(fieldName))
				wirePtrs[j] = fmt.Sprintf("ptr %s", g.intern(wire))
				schemaPtrs[j] = fmt.Sprintf("ptr %s", g.intern(schema))
			}
			names = fmt.Sprintf("@.fields.%d", i)
			wires = fmt.Sprintf("@.wires.%d", i)
			schemas = fmt.Sprintf("@.schemas.%d", i)
			fmt.Fprintf(&g.decl, "%s = private global [%d x ptr] [%s]\n", names, len(fields), strings.Join(namePtrs, ", "))
			fmt.Fprintf(&g.decl, "%s = private global [%d x ptr] [%s]\n", wires, len(fields), strings.Join(wirePtrs, ", "))
			fmt.Fprintf(&g.decl, "%s = private global [%d x ptr] [%s]\n", schemas, len(fields), strings.Join(schemaPtrs, ", "))
		}
		fmt.Fprintf(&g.out, "  call void @slick_rt_set_type(i32 %d, ptr %s, i32 %d, ptr %s, ptr %s, ptr %s, i32 %d, i32 %d)\n",
			i, g.intern(name), len(fields), names, wires, schemas, isError, native)
	}
	fmt.Fprintf(&g.out, "  call void @slick_rt_set_union_count(i32 %d)\n", len(g.unionNames))
	for i, name := range g.unionNames {
		union := g.program.unions[name]
		names := make([]string, len(union.order))
		counts := make([]string, len(union.order))
		for j, vn := range union.order {
			names[j] = fmt.Sprintf("ptr %s", g.intern(union.variants[vn].name))
			counts[j] = fmt.Sprintf("i32 %d", len(union.variants[vn].fields))
		}
		ns, cs := "null", "null"
		if len(names) > 0 {
			ns = fmt.Sprintf("@.unames.%d", i)
			cs = fmt.Sprintf("@.ucounts.%d", i)
			fmt.Fprintf(&g.decl, "%s = private global [%d x ptr] [%s]\n", ns, len(names), strings.Join(names, ", "))
			fmt.Fprintf(&g.decl, "%s = private global [%d x i32] [%s]\n", cs, len(counts), strings.Join(counts, ", "))
		}
		fmt.Fprintf(&g.out, "  call void @slick_rt_set_union(i32 %d, ptr %s, i32 %d, ptr %s, ptr %s)\n",
			i, g.intern(name), len(union.order), ns, cs)
	}
	g.out.WriteString("  ret void\n}\n\n")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
