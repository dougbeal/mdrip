# mdrip

[![Build Status](https://travis-ci.org/dougbeal/mdrip.svg?branch=master)](https://travis-ci.org/dougbeal/mdrip)

`mdrip` rips labeled command blocks from markdown files for execution.

It allows one to place markdown-based documentation under test, or (equivalently) structure
high level _integration tests_ as shell commands in a self-documenting markdown file.

## Execution

`mdrip` accepts any number of _file name_
arguments, where the files are assumed to contain markdown.

> `mdrip [--label {label}] {file.md} [{file2.md}...]`

It scans the files for
[fenced code blocks](https://help.github.com/articles/github-flavored-markdown/#fenced-code-blocks)
immediately preceded by an HTML comment with embedded _@labels_.

If one of the block labels matches the `--label` flag argument,
the associated block is extracted.

Extracted blocks are concatenated to `stdout`, or, if `--subshell` is
specified, concatenated to a bash subprocess.

This is a markdown-based instance of language-independent
[literate programming](http://en.wikipedia.org/wiki/Literate_programming).
It's language independent because shell scripts can
make, build and run programs in any programming language, via [_here_
documents](http://tldp.org/LDP/abs/html/here-docs.html) and what not.

## Build

Assuming Go installed:

```
export MDRIP=~/mdrip
GOPATH=$MDRIP/go go get github.com/monopole/mdrip
GOPATH=$MDRIP/go go test github.com/monopole/mdrip/...
$MDRIP/go/bin/mdrip   # Shows usage.
```

## Example

Consider this short [Go tutorial][example-tutorial]. (raw markdown [here][raw-example])
has bash code blocks that write, compile and run a Go program.

Send code from that file to `stdout`:

```
$MDRIP/go/bin/mdrip lesson1 \
    $MDRIP/go/src/github.com/monopole/mdrip/data/example_tutorial.md
```

Alternatively, run its code in a subshell:
```
$MDRIP/go/bin/mdrip --subshell lesson1 \
    $MDRIP/go/src/github.com/monopole/mdrip/data/example_tutorial.md
```

The above command has no output and exits with status zero if all the
scripts labelled `@lesson1` in the given markdown succeed.  On any
failure, however, the command dumps a report and exits with non-zero
status.

This is one way to cover documentation with feature tests.  Keeping
code and documentation describing the code in the same file makes it
much easier to keep them in sync.


## Details

A _script_ is a sequence of code blocks with a common label.  If a
block has multiple labels, it can be incorporated into multiple
scripts.  If a block has no label, it's ignored.  The number of
scripts that can be extracted from a set of markdown files equals the
number of unique labels.

If code blocks are in bash syntax, and the tool is itself running
in a bash shell, then piping `mdrip` output to `source /dev/stdin` is
equivalent to a human copy/pasting code blocks to their own shell
prompt.  In this scenario, an error in block _N_ will not stop
execution of block _N+1_.  To instead stop on error, pipe the output
to `bash -e`.

Alternatively, the tool can itself run extracted code in a bash subshell like this

> `mdrip --subshell someLabel file1.md file2.md ...`

If that command fails, so did something in a command block.  `mdrip`
reports which block failed and what that block's `stdout` and `stderr`
saw, while otherwise capturing and discarding subshell output.

There's no notion of encapsulation.  Also, there's no automatic
cleanup.  A block that does cleanup can be added to the markdown.

### Special labels

 * The first label on a block is slightly special, in that it's
   reported as the block name for logging.  But like any label it can
   be used for selection too.

 * The @sleep label causes mdrip to insert a `sleep 2` command after
   the block.  Appropriate if one is starting a server in the
   background in that block.

[travis-mdrip]: https://travis-ci.org/monopole/mdrip
[example-tutorial]: https://github.com/monopole/mdrip/blob/master/data/example_tutorial.md
[raw-example]: https://raw.githubusercontent.com/monopole/mdrip/master/data/example_tutorial.md

