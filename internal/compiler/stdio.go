package compiler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	stdioChunkSize       = 32 * 1024
	stdioNoProgressLimit = 100
)

// nativeIOResource is the single opaque runtime representation for native
// readers and writers. Slick owns bounds and failure semantics; Go supplies
// the underlying io.Reader, io.Writer, and optional io.Closer adapters.
type nativeIOResource struct {
	reader  io.Reader
	writer  io.Writer
	closer  io.Closer
	buffer  *bytes.Buffer
	pending error
	closed  bool
}

func newNativeReaderResource(reader io.Reader, closer io.Closer) *nativeIOResource {
	return &nativeIOResource{reader: reader, closer: closer}
}

func newNativeWriterResource(writer io.Writer, closer io.Closer) *nativeIOResource {
	resource := &nativeIOResource{writer: writer, closer: closer}
	if buffer, ok := writer.(*bytes.Buffer); ok {
		resource.buffer = buffer
	}
	return resource
}

func (resource *nativeIOResource) read(maxBytes int64) ([]byte, bool, error) {
	if resource == nil || resource.closed {
		return nil, false, errors.New("reader is closed")
	}
	if maxBytes <= 0 {
		return nil, false, errors.New("MaxBytes must be greater than zero")
	}
	if resource.pending != nil {
		err := resource.pending
		resource.pending = nil
		if errors.Is(err, io.EOF) {
			return nil, true, nil
		}
		return nil, false, err
	}
	if maxBytes > stdioChunkSize {
		maxBytes = stdioChunkSize
	}
	buffer := make([]byte, int(maxBytes))
	for attempts := 0; attempts < stdioNoProgressLimit; attempts++ {
		count, err := resource.reader.Read(buffer)
		if count < 0 || count > len(buffer) {
			return nil, false, fmt.Errorf("reader returned invalid byte count %d", count)
		}
		if count > 0 {
			chunk := append([]byte(nil), buffer[:count]...)
			if err != nil {
				resource.pending = err
			}
			return chunk, false, nil
		}
		if errors.Is(err, io.EOF) {
			return nil, true, nil
		}
		if err != nil {
			return nil, false, err
		}
	}
	return nil, false, errors.New("reader made no progress")
}

func (resource *nativeIOResource) write(data []byte) error {
	if resource == nil || resource.closed {
		return errors.New("writer is closed")
	}
	if len(data) == 0 {
		return nil
	}
	count, err := resource.writer.Write(data)
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("writer made no progress")
	}
	if count != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func (resource *nativeIOResource) close(kind string) error {
	if resource == nil || resource.closed {
		return fmt.Errorf("%s is already closed", kind)
	}
	resource.closed = true
	if resource.closer != nil {
		return resource.closer.Close()
	}
	return nil
}

func (resource *nativeIOResource) snapshot() ([]byte, error) {
	if resource == nil || resource.buffer == nil {
		return nil, errors.New("resource does not expose bytes")
	}
	return append([]byte(nil), resource.buffer.Bytes()...), nil
}

func (p *program) callNativeStdIO(function *functionDecl, frame *runtimeFrame) (runtimeValue, error, bool) {
	resultType := p.resolveType(function.namespace, function.aliases, function.result)
	switch function.native {
	case nativeStdIOReaderFromBytes:
		value := append([]byte(nil), frame.locals["Value"].scalar.([]byte)...)
		return runtimeValue{typ: stdIOBytesReaderName, native: newNativeReaderResource(bytes.NewReader(value), nil)}, nil, true
	case nativeStdIOWriterToBytes:
		buffer := &bytes.Buffer{}
		return runtimeValue{typ: stdIOBytesWriterName, native: newNativeWriterResource(buffer, nil)}, nil, true
	case nativeStdIOReaderRead:
		chunk, eof, err := frame.locals["self"].native.read(frame.locals["MaxBytes"].scalar.(int64))
		if err != nil {
			return runtimeIOFailureResult(resultType, "Read", err), nil, true
		}
		optional := &runtimeOptional{present: !eof}
		if !eof {
			optional.value = runtimeValue{typ: "bytes", scalar: chunk}
		}
		return runtimeResultValue(resultType, true, runtimeValue{typ: "bytes?", optional: optional}), nil, true
	case nativeStdIOWriterWrite:
		err := frame.locals["self"].native.write(frame.locals["Data"].scalar.([]byte))
		if err != nil {
			return runtimeIOFailureResult(resultType, "Write", err), nil, true
		}
		return runtimeResultValue(resultType, true, nullRuntimeValue()), nil, true
	case nativeStdIOWriterBytes:
		value, err := frame.locals["self"].native.snapshot()
		if err != nil {
			return runtimeValue{}, err, true
		}
		return runtimeValue{typ: "bytes", scalar: value}, nil, true
	case nativeStdIOReaderClose, nativeStdIOWriterClose:
		kind := "reader"
		if function.native == nativeStdIOWriterClose {
			kind = "writer"
		}
		if err := frame.locals["self"].native.close(kind); err != nil {
			failure := runtimeIOFailure("Close", err.Error())
			message := runtimeIOFailureMessage(failure)
			return runtimeValue{}, &slickThrow{typ: stdIOFailureName, message: message, value: failure}, true
		}
		return nullRuntimeValue(), nil, true
	case nativeStdIOReadAll:
		return p.runtimeIOReadAll(frame.locals["Reader"], frame.locals["MaxBytes"].scalar.(int64), resultType), nil, true
	case nativeStdIOCopy:
		return p.runtimeIOCopy(frame.locals["Reader"], frame.locals["Writer"], frame.locals["MaxBytes"].scalar.(int64), resultType), nil, true
	default:
		return runtimeValue{}, nil, false
	}
}

func runtimeIOFailure(operation, message string) runtimeValue {
	if strings.TrimSpace(message) == "" {
		message = "operation failed"
	}
	return runtimeValue{
		typ: stdIOFailureName,
		fields: map[string]runtimeValue{
			"Operation": {typ: "string", scalar: operation},
			"Message":   {typ: "string", scalar: message},
		},
	}
}

func runtimeIOFailureResult(resultType, operation string, err error) runtimeValue {
	return runtimeResultValue(resultType, false, runtimeIOFailure(operation, err.Error()))
}

func (p *program) runtimeIOReadAll(reader runtimeValue, maxBytes int64, resultType string) runtimeValue {
	if maxBytes < 0 {
		return runtimeIOFailureResult(resultType, "ReadAll", errors.New("MaxBytes must not be negative"))
	}
	output := make([]byte, 0)
	for {
		request := boundedReadSize(maxBytes - int64(len(output)))
		chunk, eof, failure := p.runtimeIORead(reader, request)
		if failure != nil {
			return runtimeIOFailureResult(resultType, "ReadAll", failure)
		}
		if eof {
			return runtimeResultValue(resultType, true, runtimeValue{typ: "bytes", scalar: output})
		}
		if len(chunk) == 0 {
			return runtimeIOFailureResult(resultType, "ReadAll", errors.New("reader made no progress"))
		}
		if int64(len(chunk)) > maxBytes-int64(len(output)) {
			return runtimeIOFailureResult(resultType, "ReadAll", errors.New("byte limit exceeded"))
		}
		output = append(output, chunk...)
	}
}

func (p *program) runtimeIOCopy(reader, writer runtimeValue, maxBytes int64, resultType string) runtimeValue {
	if maxBytes < 0 {
		return runtimeIOFailureResult(resultType, "Copy", errors.New("MaxBytes must not be negative"))
	}
	var total int64
	for {
		chunk, eof, failure := p.runtimeIORead(reader, boundedReadSize(maxBytes-total))
		if failure != nil {
			return runtimeIOFailureResult(resultType, "Copy", failure)
		}
		if eof {
			return runtimeResultValue(resultType, true, runtimeValue{typ: "int", scalar: total})
		}
		if len(chunk) == 0 {
			return runtimeIOFailureResult(resultType, "Copy", errors.New("reader made no progress"))
		}
		remaining := maxBytes - total
		if int64(len(chunk)) > remaining {
			if remaining > 0 {
				if failure = p.runtimeIOWrite(writer, chunk[:int(remaining)]); failure != nil {
					return runtimeIOFailureResult(resultType, "Copy", failure)
				}
			}
			return runtimeIOFailureResult(resultType, "Copy", errors.New("byte limit exceeded"))
		}
		if failure = p.runtimeIOWrite(writer, chunk); failure != nil {
			return runtimeIOFailureResult(resultType, "Copy", failure)
		}
		total += int64(len(chunk))
	}
}

func boundedReadSize(remaining int64) int64 {
	if remaining < stdioChunkSize {
		return remaining + 1
	}
	return stdioChunkSize
}

func (p *program) runtimeIORead(receiver runtimeValue, maxBytes int64) ([]byte, bool, error) {
	value, err := p.callRuntimeIOMethod(receiver, "Read", []runtimeValue{{typ: "int", scalar: maxBytes}})
	if err != nil {
		return nil, false, err
	}
	if value.result == nil {
		return nil, false, errors.New("Reader.Read returned a non-Result value")
	}
	if !value.result.ok {
		return nil, false, errors.New(runtimeIOFailureMessage(value.result.payload))
	}
	payload := value.result.payload
	if payload.typ == "null" {
		return nil, true, nil
	}
	if chunk, ok := payload.scalar.([]byte); ok {
		if int64(len(chunk)) > maxBytes {
			return nil, false, errors.New("reader returned a chunk larger than MaxBytes")
		}
		return append([]byte(nil), chunk...), false, nil
	}
	if payload.optional == nil {
		return nil, false, errors.New("Reader.Read returned a non-optional success value")
	}
	if !payload.optional.present {
		return nil, true, nil
	}
	chunk, ok := payload.optional.value.scalar.([]byte)
	if !ok {
		return nil, false, errors.New("Reader.Read returned a non-bytes chunk")
	}
	if int64(len(chunk)) > maxBytes {
		return nil, false, errors.New("reader returned a chunk larger than MaxBytes")
	}
	return append([]byte(nil), chunk...), false, nil
}

func (p *program) runtimeIOWrite(receiver runtimeValue, chunk []byte) error {
	value, err := p.callRuntimeIOMethod(receiver, "Write", []runtimeValue{{typ: "bytes", scalar: append([]byte(nil), chunk...)}})
	if err != nil {
		return err
	}
	if value.result == nil {
		return errors.New("Writer.Write returned a non-Result value")
	}
	if !value.result.ok {
		return errors.New(runtimeIOFailureMessage(value.result.payload))
	}
	return nil
}

func (p *program) callRuntimeIOMethod(receiver runtimeValue, name string, arguments []runtimeValue) (runtimeValue, error) {
	class := p.classes[receiver.typ]
	if class == nil || class.implementations[name] == nil {
		return runtimeValue{}, fmt.Errorf("%s has no implemented %s method", displayName(receiver.typ), name)
	}
	return p.callFunction(class.implementations[name], arguments, &receiver, nil)
}

func runtimeIOFailureMessage(value runtimeValue) string {
	if message, ok := value.fields["Message"]; ok {
		return formatRuntimeValue(message)
	}
	return formatRuntimeValue(value)
}

func (g *goGenerator) emitNativeMethod(function *functionDecl, receiverType string) error {
	resultType, err := g.declaredType(function.namespace, function.aliases, function.result)
	if err != nil {
		return err
	}
	receiver := g.unique("self")
	arguments := make([]string, 0, len(function.params))
	parameters := make([]string, 0, len(function.params))
	for _, parameter := range function.params {
		typ, err := g.declaredType(function.namespace, function.aliases, parameter.typ)
		if err != nil {
			return err
		}
		argument := g.unique("argument")
		arguments = append(arguments, argument)
		parameters = append(parameters, argument+" "+g.goType(typ))
	}
	g.line("func (%s %s) %s(%s) (%s, error) {",
		receiver, goClassName(receiverType), goMethodName(function.name), strings.Join(parameters, ", "), g.goType(resultType))
	switch function.native {
	case nativeStdIOReaderRead:
		g.line("return slickIORead(%s.slickResource, %s), nil", receiver, arguments[0])
	case nativeStdIOReaderClose:
		g.line("return struct{}{}, slickIOClose(%s.slickResource, %q)", receiver, "reader")
	case nativeStdIOWriterWrite:
		g.line("return slickIOWrite(%s.slickResource, %s), nil", receiver, arguments[0])
	case nativeStdIOWriterBytes:
		g.line("return slickIOSnapshot(%s.slickResource), nil", receiver)
	case nativeStdIOWriterClose:
		g.line("return struct{}{}, slickIOClose(%s.slickResource, %q)", receiver, "writer")
	default:
		return fmt.Errorf("unknown native Slick method %s", function.native)
	}
	g.line("}")
	g.line("")
	return nil
}

func (g *goGenerator) emitStdIORuntime() {
	failure := goClassName(stdIOFailureName)
	operationField := goFieldName("Operation")
	messageField := goFieldName("Message")
	readResult := g.goType("Result<bytes?," + stdIOFailureName + ">")
	writeResult := g.goType("Result<null," + stdIOFailureName + ">")
	readAllResult := g.goType("Result<bytes," + stdIOFailureName + ">")
	copyResult := g.goType("Result<int," + stdIOFailureName + ">")
	readerInterface := goInterfaceName(stdIOReaderName)
	writerInterface := goInterfaceName(stdIOWriterName)

	g.line(`type slickIOResource struct {`)
	g.line(`reader io.Reader`)
	g.line(`writer io.Writer`)
	g.line(`closer io.Closer`)
	g.line(`buffer *bytes.Buffer`)
	g.line(`pending error`)
	g.line(`closed bool`)
	g.line(`}`)
	g.line(`func slickIONewReader(value slickBytes) *slickIOResource {`)
	g.line(`snapshot := append(slickBytes(nil), value...)`)
	g.line(`return &slickIOResource{reader: bytes.NewReader(snapshot)}`)
	g.line(`}`)
	g.line(`func slickIONewWriter() *slickIOResource {`)
	g.line(`buffer := &bytes.Buffer{}`)
	g.line(`return &slickIOResource{writer: buffer, buffer: buffer}`)
	g.line(`}`)
	g.line("func slickIOFailure(operation, message string) *%s {", failure)
	g.line(`if strings.TrimSpace(message) == "" { message = "operation failed" }`)
	g.line("return &%s{%s: operation, %s: message}", failure, operationField, messageField)
	g.line(`}`)
	g.line("func slickIORead(resource *slickIOResource, maxBytes int64) %s {", readResult)
	g.line("if resource == nil || resource.closed { return %s{failure: slickIOFailure(%q, %q)} }", readResult, "Read", "reader is closed")
	g.line("if maxBytes <= 0 { return %s{failure: slickIOFailure(%q, %q)} }", readResult, "Read", "MaxBytes must be greater than zero")
	g.line(`if resource.pending != nil {`)
	g.line(`err := resource.pending`)
	g.line(`resource.pending = nil`)
	g.line("if errors.Is(err, io.EOF) { return %s{ok: true, value: slickNone[slickBytes]()} }", readResult)
	g.line("return %s{failure: slickIOFailure(%q, err.Error())}", readResult, "Read")
	g.line(`}`)
	g.line("if maxBytes > %d { maxBytes = %d }", stdioChunkSize, stdioChunkSize)
	g.line(`buffer := make(slickBytes, int(maxBytes))`)
	g.line("for attempts := 0; attempts < %d; attempts++ {", stdioNoProgressLimit)
	g.line(`count, err := resource.reader.Read(buffer)`)
	g.line("if count < 0 || count > len(buffer) { return %s{failure: slickIOFailure(%q, fmt.Sprintf(%q, count))} }",
		readResult, "Read", "reader returned invalid byte count %d")
	g.line(`if count > 0 {`)
	g.line(`chunk := append(slickBytes(nil), buffer[:count]...)`)
	g.line(`if err != nil { resource.pending = err }`)
	g.line("return %s{ok: true, value: slickSome(chunk)}", readResult)
	g.line(`}`)
	g.line("if errors.Is(err, io.EOF) { return %s{ok: true, value: slickNone[slickBytes]()} }", readResult)
	g.line("if err != nil { return %s{failure: slickIOFailure(%q, err.Error())} }", readResult, "Read")
	g.line(`}`)
	g.line("return %s{failure: slickIOFailure(%q, %q)}", readResult, "Read", "reader made no progress")
	g.line(`}`)
	g.line("func slickIOWrite(resource *slickIOResource, data slickBytes) %s {", writeResult)
	g.line("if resource == nil || resource.closed { return %s{failure: slickIOFailure(%q, %q)} }", writeResult, "Write", "writer is closed")
	g.line("if len(data) == 0 { return %s{ok: true, value: struct{}{}} }", writeResult)
	g.line(`count, err := resource.writer.Write(data)`)
	g.line("if err != nil { return %s{failure: slickIOFailure(%q, err.Error())} }", writeResult, "Write")
	g.line("if count == 0 { return %s{failure: slickIOFailure(%q, %q)} }", writeResult, "Write", "writer made no progress")
	g.line("if count != len(data) { return %s{failure: slickIOFailure(%q, io.ErrShortWrite.Error())} }", writeResult, "Write")
	g.line("return %s{ok: true, value: struct{}{}}", writeResult)
	g.line(`}`)
	g.line(`func slickIOClose(resource *slickIOResource, kind string) error {`)
	g.line(`if resource == nil || resource.closed { return slickIOFailure("Close", kind + " is already closed") }`)
	g.line(`resource.closed = true`)
	g.line(`if resource.closer != nil { if err := resource.closer.Close(); err != nil { return slickIOFailure("Close", err.Error()) } }`)
	g.line(`return nil`)
	g.line(`}`)
	g.line(`func slickIOSnapshot(resource *slickIOResource) slickBytes {`)
	g.line(`if resource == nil || resource.buffer == nil { return nil }`)
	g.line(`return append(slickBytes(nil), resource.buffer.Bytes()...)`)
	g.line(`}`)
	g.line(`func slickIOReadSize(remaining int64) int64 {`)
	g.line("if remaining < %d { return remaining + 1 }", stdioChunkSize)
	g.line("return %d", stdioChunkSize)
	g.line(`}`)
	g.line("func slickIOReadAll(reader %s, maxBytes int64) (%s, error) {", readerInterface, readAllResult)
	g.line("if maxBytes < 0 { return %s{failure: slickIOFailure(%q, %q)}, nil }", readAllResult, "ReadAll", "MaxBytes must not be negative")
	g.line(`output := make(slickBytes, 0)`)
	g.line(`for {`)
	g.line(`request := slickIOReadSize(maxBytes - int64(len(output)))`)
	g.line("read, err := reader.%s(request)", goMethodName("Read"))
	g.line("if err != nil { return %s{}, err }", readAllResult)
	g.line("if !read.ok { return %s{failure: slickIOFailure(%q, read.failure.%s)}, nil }", readAllResult, "ReadAll", messageField)
	g.line("if !read.value.present { return %s{ok: true, value: output}, nil }", readAllResult)
	g.line(`chunk := read.value.value`)
	g.line("if int64(len(chunk)) > request { return %s{failure: slickIOFailure(%q, %q)}, nil }", readAllResult, "ReadAll", "reader returned a chunk larger than MaxBytes")
	g.line("if len(chunk) == 0 { return %s{failure: slickIOFailure(%q, %q)}, nil }", readAllResult, "ReadAll", "reader made no progress")
	g.line("if int64(len(chunk)) > maxBytes-int64(len(output)) { return %s{failure: slickIOFailure(%q, %q)}, nil }", readAllResult, "ReadAll", "byte limit exceeded")
	g.line(`output = append(output, chunk...)`)
	g.line(`}`)
	g.line(`}`)
	g.line("func slickIOCopy(reader %s, writer %s, maxBytes int64) (%s, error) {", readerInterface, writerInterface, copyResult)
	g.line("if maxBytes < 0 { return %s{failure: slickIOFailure(%q, %q)}, nil }", copyResult, "Copy", "MaxBytes must not be negative")
	g.line(`var total int64`)
	g.line(`for {`)
	g.line(`request := slickIOReadSize(maxBytes - total)`)
	g.line("read, err := reader.%s(request)", goMethodName("Read"))
	g.line("if err != nil { return %s{}, err }", copyResult)
	g.line("if !read.ok { return %s{failure: slickIOFailure(%q, read.failure.%s)}, nil }", copyResult, "Copy", messageField)
	g.line("if !read.value.present { return %s{ok: true, value: total}, nil }", copyResult)
	g.line(`chunk := read.value.value`)
	g.line("if int64(len(chunk)) > request { return %s{failure: slickIOFailure(%q, %q)}, nil }", copyResult, "Copy", "reader returned a chunk larger than MaxBytes")
	g.line("if len(chunk) == 0 { return %s{failure: slickIOFailure(%q, %q)}, nil }", copyResult, "Copy", "reader made no progress")
	g.line(`remaining := maxBytes - total`)
	g.line(`if int64(len(chunk)) > remaining {`)
	g.line(`if remaining > 0 {`)
	g.line("written, err := writer.%s(append(slickBytes(nil), chunk[:int(remaining)]...))", goMethodName("Write"))
	g.line("if err != nil { return %s{}, err }", copyResult)
	g.line("if !written.ok { return %s{failure: slickIOFailure(%q, written.failure.%s)}, nil }", copyResult, "Copy", messageField)
	g.line(`}`)
	g.line("return %s{failure: slickIOFailure(%q, %q)}, nil", copyResult, "Copy", "byte limit exceeded")
	g.line(`}`)
	g.line("written, err := writer.%s(append(slickBytes(nil), chunk...))", goMethodName("Write"))
	g.line("if err != nil { return %s{}, err }", copyResult)
	g.line("if !written.ok { return %s{failure: slickIOFailure(%q, written.failure.%s)}, nil }", copyResult, "Copy", messageField)
	g.line(`total += int64(len(chunk))`)
	g.line(`}`)
	g.line(`}`)
	g.line("")
}
