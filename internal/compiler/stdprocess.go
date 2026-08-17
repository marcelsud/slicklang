package compiler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

const (
	nativeStdProcessRun runtimeOperationID = "std.process.Run"

	stdProcessStatusName    = "std.process.Status"
	stdProcessCompletedName = "std.process.Completed"
	stdProcessFailureName   = "std.process.Failure"

	// minExitCode and maxExitCode bound the portable exit status a CLI main may
	// return; a Status outside the range is a deterministic runtime failure.
	minExitCode int64 = 0
	maxExitCode int64 = 255

	// invalidExitCodeMessage is shared by the interpreter and the generated
	// entry point so both report an out-of-range Status identically.
	invalidExitCodeMessage = "std.process.Status ExitCode must be 0 through 255"
)

// ProcessStatus is the explicit command-line result of a main that returns
// std.process.Status.
type ProcessStatus struct {
	ExitCode    int
	Output      []byte
	ErrorOutput []byte
}

// Outcome is what one Slick program produced. Text holds the formatted value of
// a display-returning main. Status is set whenever main returned
// std.process.Status, including when its exit code is out of range and an error
// is reported as well: the runner writes Output and ErrorOutput first, exactly
// as it would for a valid Status, and only then reports the failure.
type Outcome struct {
	Text   string
	Status *ProcessStatus
}

func invalidExitCodeError(code int64) error {
	return fmt.Errorf("%s, found %d", invalidExitCodeMessage, code)
}

// mainAcceptsArguments reports whether root.main takes the command-line
// argument vector. Any other parameter list is rejected for both backends.
func (p *program) mainAcceptsArguments(main *functionDecl) (bool, error) {
	switch len(main.params) {
	case 0:
		return false, nil
	case 1:
		if p.resolveType(main.namespace, main.aliases, main.params[0].typ) == "string[]" {
			return true, nil
		}
	}
	return false, errors.New("root.main must accept no parameters or one string[] parameter")
}

// processStatusFromRuntime converts an interpreted std.process.Status into the
// runner-facing status, reporting an out-of-range exit code as a runtime
// failure while still returning the bytes the program produced.
func processStatusFromRuntime(value runtimeValue) (*ProcessStatus, error) {
	code := value.fields["ExitCode"].scalar.(int64)
	status := &ProcessStatus{
		ExitCode:    int(code),
		Output:      runtimeBytesField(value, "Output"),
		ErrorOutput: runtimeBytesField(value, "ErrorOutput"),
	}
	if code < minExitCode || code > maxExitCode {
		return status, invalidExitCodeError(code)
	}
	return status, nil
}

func runtimeBytesField(value runtimeValue, name string) []byte {
	data, _ := value.fields[name].scalar.([]byte)
	return data
}

type processCompletedData struct {
	exitCode    int64
	output      []byte
	errorOutput []byte
}

type processFailureData struct {
	operation string
	program   string
	message   string
}

// processCapture collects a child's stdout and stderr under one combined byte
// limit. Writes never fail: a failed write would stop the copier draining the
// pipe and leave the child blocked forever, so an overflowing child is killed
// and its remaining output discarded instead.
type processCapture struct {
	mutex       sync.Mutex
	limit       int64
	total       int64
	overflow    bool
	output      []byte
	errorOutput []byte
	process     *os.Process
}

func (capture *processCapture) write(data []byte, isError bool) (int, error) {
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	remaining := capture.limit - capture.total
	accepted := int64(len(data))
	if accepted > remaining {
		capture.overflow = true
		accepted = remaining
	}
	if accepted > 0 {
		if isError {
			capture.errorOutput = append(capture.errorOutput, data[:accepted]...)
		} else {
			capture.output = append(capture.output, data[:accepted]...)
		}
		capture.total += accepted
	}
	if capture.overflow {
		capture.terminate()
	}
	return len(data), nil
}

// arm records the started process and kills it immediately when output already
// overflowed between Start and this call.
func (capture *processCapture) arm(process *os.Process) {
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	capture.process = process
	if capture.overflow {
		capture.terminate()
	}
}

// terminate must be called with the mutex held. Killing an already-exited
// process is not a failure, so its error is discarded.
func (capture *processCapture) terminate() {
	if capture.process != nil {
		_ = capture.process.Kill()
	}
}

func (capture *processCapture) overflowed() bool {
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	return capture.overflow
}

type processWriter struct {
	capture *processCapture
	isError bool
}

func (writer *processWriter) Write(data []byte) (int, error) {
	return writer.capture.write(data, writer.isError)
}

func processFailure(operation, program, message string) *processFailureData {
	return &processFailureData{operation: operation, program: program, message: message}
}

// runProcess executes program directly with the exact argument vector. It never
// consults a shell, always waits for the child, and signals and reaps the child
// when ctx is cancelled.
func runProcess(ctx context.Context, program string, arguments []string, workingDirectory string, hasWorkingDirectory bool, maxOutputBytes int64) (processCompletedData, *processFailureData) {
	if maxOutputBytes < 0 {
		return processCompletedData{}, processFailure("OutputLimit", program, "MaxOutputBytes must not be negative")
	}
	if ctx.Err() != nil {
		return processCompletedData{}, processFailure("Cancelled", program, "operation cancelled before child start")
	}
	command := exec.CommandContext(ctx, program, arguments...)
	if hasWorkingDirectory {
		info, err := os.Stat(workingDirectory)
		if err != nil || !info.IsDir() {
			return processCompletedData{}, processFailure("WorkingDirectory", program, "working directory is not an existing directory")
		}
		command.Dir = workingDirectory
	}
	if ctx.Err() != nil {
		return processCompletedData{}, processFailure("Cancelled", program, "operation cancelled before child start")
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return processCompletedData{}, processFailure("Spawn", program, err.Error())
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		return processCompletedData{}, processFailure("Spawn", program, err.Error())
	}
	closePipes := func() {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		_ = stderrRead.Close()
		_ = stderrWrite.Close()
	}
	capture := &processCapture{limit: maxOutputBytes}
	command.Stdout = stdoutWrite
	command.Stderr = stderrWrite
	if err := command.Start(); err != nil {
		closePipes()
		if ctx.Err() != nil {
			return processCompletedData{}, processFailure("Cancelled", program, "operation cancelled before child start")
		}
		return processCompletedData{}, processFailure("Spawn", program, err.Error())
	}
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	capture.arm(command.Process)
	var copies sync.WaitGroup
	copies.Add(2)
	go func() {
		defer copies.Done()
		_, _ = io.Copy(&processWriter{capture: capture}, stdoutRead)
	}()
	go func() {
		defer copies.Done()
		_, _ = io.Copy(&processWriter{capture: capture, isError: true}, stderrRead)
	}()
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	var waitError error
	select {
	case waitError = <-waited:
	case <-ctx.Done():
		_ = stdoutRead.Close()
		_ = stderrRead.Close()
		waitError = <-waited
	}
	if ctx.Err() != nil {
		_ = stdoutRead.Close()
		_ = stderrRead.Close()
	}
	copies.Wait()
	_ = stdoutRead.Close()
	_ = stderrRead.Close()
	if ctx.Err() != nil {
		return processCompletedData{}, processFailure("Cancelled", program, "operation cancelled; child process was signalled")
	}
	if capture.overflowed() {
		return processCompletedData{}, processFailure("OutputLimit", program, fmt.Sprintf("captured output exceeds %d bytes", maxOutputBytes))
	}
	if waitError != nil {
		var exitError *exec.ExitError
		if !errors.As(waitError, &exitError) {
			return processCompletedData{}, processFailure("Wait", program, waitError.Error())
		}
		if !exitError.ProcessState.Exited() {
			return processCompletedData{}, processFailure("Signal", program, "child process was terminated by a signal")
		}
	}
	return processCompletedData{
		exitCode:    int64(command.ProcessState.ExitCode()),
		output:      capture.output,
		errorOutput: capture.errorOutput,
	}, nil
}

func runtimeProcessFailure(resultType string, failure *processFailureData) runtimeValue {
	value := runtimeValue{typ: stdProcessFailureName, fields: map[string]runtimeValue{
		"Operation": {typ: "string", scalar: failure.operation},
		"Program":   {typ: "string", scalar: failure.program},
		"Message":   {typ: "string", scalar: failure.message},
	}}
	return runtimeResultValue(resultType, false, value)
}

func runtimeProcessCompleted(resultType string, completed processCompletedData) runtimeValue {
	value := runtimeValue{typ: stdProcessCompletedName, fields: map[string]runtimeValue{
		"ExitCode":    {typ: "int", scalar: completed.exitCode},
		"Output":      {typ: "bytes", scalar: completed.output},
		"ErrorOutput": {typ: "bytes", scalar: completed.errorOutput},
	}}
	return runtimeResultValue(resultType, true, value)
}

func (p *program) callNativeStdProcess(function *functionDecl, frame *runtimeFrame) (runtimeValue, error, bool) {
	if function.native != nativeStdProcessRun {
		return runtimeValue{}, nil, false
	}
	resultType := p.resolveType(function.namespace, function.aliases, function.result)
	programName := frame.locals["Program"].scalar.(string)
	elements := frame.locals["Arguments"].elements
	arguments := make([]string, len(elements))
	for index, element := range elements {
		arguments[index] = element.scalar.(string)
	}
	directory := ""
	present, hasDirectory := runtimePresentValue(frame.locals["WorkingDirectory"])
	if hasDirectory {
		directory = present.scalar.(string)
	}
	completed, failure := runProcess(frame.ctx, programName, arguments, directory, hasDirectory, frame.locals["MaxOutputBytes"].scalar.(int64))
	if failure != nil {
		return runtimeProcessFailure(resultType, failure), nil, true
	}
	return runtimeProcessCompleted(resultType, completed), nil, true
}

// emitProcessRuntimeSupport mirrors runProcess in the generated program so a
// standalone binary and the interpreter agree on bytes, exit codes, and
// failure shape.
func (g *goGenerator) emitProcessRuntimeSupport() {
	completedClass := goClassName(stdProcessCompletedName)
	failureClass := goClassName(stdProcessFailureName)
	resultType := g.goType("Result<" + stdProcessCompletedName + "," + stdProcessFailureName + ">")

	g.line(`type slickProcessFailureData struct { operation string; program string; message string }`)
	g.line(`type slickProcessCompletedData struct { exitCode int64; output slickBytes; errorOutput slickBytes }`)
	g.line(`type slickProcessCapture struct { mutex sync.Mutex; limit int64; total int64; overflow bool; output []byte; errorOutput []byte; process *os.Process }`)
	// Writes never fail: a failing write stops the pipe copier and can block the
	// child forever, so overflow kills the child and discards the excess.
	g.line(`func (capture *slickProcessCapture) write(data []byte, isError bool) (int, error) {`)
	g.line(`capture.mutex.Lock(); defer capture.mutex.Unlock()`)
	g.line(`remaining := capture.limit - capture.total; accepted := int64(len(data))`)
	g.line(`if accepted > remaining { capture.overflow = true; accepted = remaining }`)
	g.line(`if accepted > 0 {`)
	g.line(`if isError { capture.errorOutput = append(capture.errorOutput, data[:accepted]...) } else { capture.output = append(capture.output, data[:accepted]...) }`)
	g.line(`capture.total += accepted`)
	g.line(`}`)
	g.line(`if capture.overflow { capture.terminate() }`)
	g.line(`return len(data), nil`)
	g.line(`}`)
	g.line(`func (capture *slickProcessCapture) arm(process *os.Process) {`)
	g.line(`capture.mutex.Lock(); defer capture.mutex.Unlock()`)
	g.line(`capture.process = process`)
	g.line(`if capture.overflow { capture.terminate() }`)
	g.line(`}`)
	g.line(`func (capture *slickProcessCapture) terminate() { if capture.process != nil { _ = capture.process.Kill() } }`)
	g.line(`func (capture *slickProcessCapture) overflowed() bool { capture.mutex.Lock(); defer capture.mutex.Unlock(); return capture.overflow }`)
	g.line(`type slickProcessWriter struct { capture *slickProcessCapture; isError bool }`)
	g.line(`func (writer *slickProcessWriter) Write(data []byte) (int, error) { return writer.capture.write(data, writer.isError) }`)
	g.line(`func slickProcessFailure(operation, program, message string) *slickProcessFailureData { return &slickProcessFailureData{operation: operation, program: program, message: message} }`)
	g.line(`func slickProcessPerform(ctx context.Context, program string, arguments []string, workingDirectory string, hasWorkingDirectory bool, maxOutputBytes int64) (slickProcessCompletedData, *slickProcessFailureData) {`)
	g.line(`if maxOutputBytes < 0 { return slickProcessCompletedData{}, slickProcessFailure("OutputLimit", program, "MaxOutputBytes must not be negative") }`)
	g.line(`if ctx.Err() != nil { return slickProcessCompletedData{}, slickProcessFailure("Cancelled", program, "operation cancelled before child start") }`)
	g.line(`command := exec.CommandContext(ctx, program, arguments...)`)
	g.line(``)
	g.line(`if hasWorkingDirectory {`)
	g.line(`info, err := os.Stat(workingDirectory)`)
	g.line(`if err != nil || !info.IsDir() { return slickProcessCompletedData{}, slickProcessFailure("WorkingDirectory", program, "working directory is not an existing directory") }`)
	g.line(`command.Dir = workingDirectory`)
	g.line(`}`)
	g.line(`if ctx.Err() != nil { return slickProcessCompletedData{}, slickProcessFailure("Cancelled", program, "operation cancelled before child start") }`)
	g.line(`stdoutRead, stdoutWrite, err := os.Pipe()`)
	g.line(`if err != nil { return slickProcessCompletedData{}, slickProcessFailure("Spawn", program, err.Error()) }`)
	g.line(`stderrRead, stderrWrite, err := os.Pipe()`)
	g.line(`if err != nil { _ = stdoutRead.Close(); _ = stdoutWrite.Close(); return slickProcessCompletedData{}, slickProcessFailure("Spawn", program, err.Error()) }`)
	g.line(`closePipes := func() { _ = stdoutRead.Close(); _ = stdoutWrite.Close(); _ = stderrRead.Close(); _ = stderrWrite.Close() }`)
	g.line(`capture := &slickProcessCapture{limit: maxOutputBytes}`)
	g.line(`command.Stdout = stdoutWrite`)
	g.line(`command.Stderr = stderrWrite`)
	g.line(`if err := command.Start(); err != nil { closePipes(); if ctx.Err() != nil { return slickProcessCompletedData{}, slickProcessFailure("Cancelled", program, "operation cancelled before child start") }; return slickProcessCompletedData{}, slickProcessFailure("Spawn", program, err.Error()) }`)
	g.line(`_ = stdoutWrite.Close(); _ = stderrWrite.Close()`)
	g.line(`capture.arm(command.Process)`)
	g.line(`var copies sync.WaitGroup; copies.Add(2)`)
	g.line(`go func() { defer copies.Done(); _, _ = io.Copy(&slickProcessWriter{capture: capture}, stdoutRead) }()`)
	g.line(`go func() { defer copies.Done(); _, _ = io.Copy(&slickProcessWriter{capture: capture, isError: true}, stderrRead) }()`)
	g.line(`waited := make(chan error, 1); go func() { waited <- command.Wait() }()`)
	g.line(`var waitError error`)
	g.line(`select { case waitError = <-waited: case <-ctx.Done(): _ = stdoutRead.Close(); _ = stderrRead.Close(); waitError = <-waited }`)
	g.line(`if ctx.Err() != nil { _ = stdoutRead.Close(); _ = stderrRead.Close() }`)
	g.line(`copies.Wait(); _ = stdoutRead.Close(); _ = stderrRead.Close()`)
	g.line(`if ctx.Err() != nil { return slickProcessCompletedData{}, slickProcessFailure("Cancelled", program, "operation cancelled; child process was signalled") }`)
	g.line(`if capture.overflowed() { return slickProcessCompletedData{}, slickProcessFailure("OutputLimit", program, fmt.Sprintf("captured output exceeds %%d bytes", maxOutputBytes)) }`)
	g.line(`if waitError != nil {`)
	g.line(`var exitError *exec.ExitError`)
	g.line(`if !errors.As(waitError, &exitError) { return slickProcessCompletedData{}, slickProcessFailure("Wait", program, waitError.Error()) }`)
	g.line(`if !exitError.ProcessState.Exited() { return slickProcessCompletedData{}, slickProcessFailure("Signal", program, "child process was terminated by a signal") }`)
	g.line(`}`)
	g.line(`return slickProcessCompletedData{exitCode: int64(command.ProcessState.ExitCode()), output: slickBytes(capture.output), errorOutput: slickBytes(capture.errorOutput)}, nil`)
	g.line(`}`)
	g.line("func slickProcessRun(ctx context.Context, program string, arguments []string, workingDirectory slickOptional[string], maxOutputBytes int64) (%s, error) {", resultType)
	g.line(`completed, failure := slickProcessPerform(ctx, program, arguments, workingDirectory.value, workingDirectory.present, maxOutputBytes)`)
	g.line("if failure != nil { return %s{failure: &%s{%s: failure.operation, %s: failure.program, %s: failure.message}}, nil }",
		resultType, failureClass,
		goFieldName("Operation"), goFieldName("Program"), goFieldName("Message"))
	g.line("return %s{ok: true, value: %s{%s: completed.exitCode, %s: completed.output, %s: completed.errorOutput}}, nil",
		resultType, completedClass,
		goFieldName("ExitCode"), goFieldName("Output"), goFieldName("ErrorOutput"))
	g.line(`}`)
	g.line("")
}
