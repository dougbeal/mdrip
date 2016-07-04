package model

import (
	"fmt"
	"os"
	"strings"
)

type status int

const (
	yep status = iota
	nope
)

// BlockOutput pairs status (success or failure) with the output
// collected from a stream (i.e. stderr or stdout) as a result of
// executing all or part of a command block.
//
// Output can appear on stderr without neccessarily being associated
// with shell failure.
type BlockOutput struct {
	success status
	output  string
}

func (x BlockOutput) Succeeded() bool {
	return x.success == yep
}

func (x BlockOutput) GetOutput() string {
	return x.output
}

func NewFailureOutput(output string) *BlockOutput {
	return &BlockOutput{nope, output}
}

func NewSuccessOutput(output string) *BlockOutput {
	return &BlockOutput{yep, output}
}

// ScriptResult pairs BlockOutput with meta data about shell execution.
type ScriptResult struct {
	BlockOutput
	fileName string        // File in which the error occurred.
	index    int           // Command block index.
	block    *CommandBlock // Content of actual command block.
	problem  error         // Error, if any.
	message  string        // Detailed error message, if any.
}

func NewScriptResult() *ScriptResult {
	noLabels := []Label{}
	blockOutput := NewFailureOutput("")
	return &ScriptResult{*blockOutput, "", -1, NewCommandBlock(noLabels, ""), nil, ""}
}

// For tests.
func NoCommandsScriptResult(blockOutput *BlockOutput, fileName string, index int, message string) *ScriptResult {
	noLabels := []Label{}
	return &ScriptResult{*blockOutput, fileName, index, NewCommandBlock(noLabels, ""), nil, message}
}

func (x *ScriptResult) GetFileName() string {
	return x.fileName
}

func (x *ScriptResult) GetProblem() error {
	return x.problem
}

func (x *ScriptResult) SetProblem(e error) *ScriptResult {
	x.problem = e
	return x
}

func (x *ScriptResult) GetMessage() string {
	return x.message
}

func (x *ScriptResult) SetMessage(m string) *ScriptResult {
	x.message = m
	return x
}

func (x *ScriptResult) SetOutput(m string) *ScriptResult {
	x.output = m
	return x
}

func (x *ScriptResult) GetIndex() int {
	return x.index
}

func (x *ScriptResult) SetIndex(i int) *ScriptResult {
	x.index = i
	return x
}

func (x *ScriptResult) SetBlock(b *CommandBlock) *ScriptResult {
	x.block = b
	return x
}

func (x *ScriptResult) SetFileName(n string) *ScriptResult {
	x.fileName = n
	return x
}

// Complain spits the contents of a ScriptResult to stderr.
func (x *ScriptResult) Dump(selectedLabel Label) {
	delim := strings.Repeat("-", 70) + "\n"
	fmt.Fprintf(os.Stderr, delim)
	x.block.Dump(os.Stderr, "Error", x.index+1, selectedLabel, x.fileName)
	fmt.Fprintf(os.Stderr, delim)
	dumpCapturedOutput("Stdout", delim, x.output)
	if len(x.message) > 0 {
		dumpCapturedOutput("Stderr", delim, x.message)
	}
}

func dumpCapturedOutput(name, delim, output string) {
	fmt.Fprintf(os.Stderr, "\n%s capture:\n", name)
	fmt.Fprintf(os.Stderr, delim)
	fmt.Fprintf(os.Stderr, output)
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, delim)
}