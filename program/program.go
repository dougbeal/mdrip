package program

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/monopole/mdrip/scanner"
	"github.com/monopole/mdrip/util"
	"github.com/monopole/mdrip/lexer"
	"github.com/monopole/mdrip/model"
)

// Program is a list of scripts, each from their own file.
type Program struct {
	blockTimeout time.Duration
	label        model.Label
	fileNames    []model.FileName
	Scripts      []*model.Script
}

const (
	tmplNameProgram = "program"
	tmplBodyProgram = `
{{define "` + tmplNameProgram + `"}}
{{range $i, $s := .AllScripts}}
  <div data-id="{{$i}}">
  {{ template "` + model.TmplNameScript + `" $s }}
  </div>
{{end}}
{{end}}
`
)

var templates = template.Must(
	template.New("main").Parse(
		model.TmplBodyCommandBlock + model.TmplBodyScript + tmplBodyProgram))

func NewProgram(timeout time.Duration, label model.Label, fileNames []model.FileName) *Program {
	return &Program{timeout, label, fileNames, []*model.Script{}}
}

// Build program code from blocks extracted from markdown files.
func (p *Program) Reload() {
	p.Scripts = []*model.Script{}
	for _, fileName := range p.fileNames {
		contents, err := ioutil.ReadFile(string(fileName))
		if err != nil {
			glog.Warning("Unable to read file \"%s\".", fileName)
		}
		m := lexer.Parse(string(contents))
		if blocks, ok := m[p.label]; ok {
			p.Add(model.NewScript(fileName, blocks))
		}
	}

	if p.ScriptCount() < 1 {
		if p.label.IsAny() {
			glog.Fatal("No blocks found in the given files.")
		} else {
			glog.Fatalf("No blocks labelled %q found in the given files.", p.label)
		}
	}
}

func (p *Program) Add(s *model.Script) *Program {
	p.Scripts = append(p.Scripts, s)
	return p
}

// Exported only for the template.
func (p *Program) AllScripts() []*model.Script {
	return p.Scripts
}

func (p *Program) ScriptCount() int {
	return len(p.Scripts)
}

// PrintNormal simply prints the contents of a program.
func (p Program) PrintNormal(w io.Writer) {
	for _, s := range p.Scripts {
		s.Print(w, p.label, 0)
	}
	fmt.Fprintf(w, "echo \" \"\n")
	fmt.Fprintf(w, "echo \"All done.  No errors.\"\n")
}

// PrintPreambled emits the first n blocks of a script normally, then
// emits the n blocks _again_, as well as all the remaining scripts,
// so that they run in a subshell with signal handling.
//
// This allows the aggregrate script to be structured as 1) a preamble
// initialization script that impacts the environment of the active
// shell, followed by 2) a script that executes as a subshell that
// exits on error.  An exit in (2) won't cause the active shell
// to close (annoying if it is a terminal).
//
// It's up to the markdown author to assure that the n blocks can
// always complete without exit on error because they will run in the
// existing terminal.  Hence these blocks should just set environment
// variables and/or define shell functions.
//
// The goal is to let the user both modify their existing terminal
// environment, and run remaining code in a trapped subshell, and
// survive any errors in that subshell with a modified environment.
func (p Program) PrintPreambled(w io.Writer, n int) {
	// Write the first n blocks if the first script normally.
	p.Scripts[0].Print(w, p.label, n)
	// Followed by everything appearing in a bash subshell.
	hereDocName := "HANDLED_SCRIPT"
	fmt.Fprintf(w, " bash -euo pipefail <<'%s'\n", hereDocName)
	fmt.Fprintf(w, "function handledTrouble() {\n")
	fmt.Fprintf(w, "  echo \" \"\n")
	fmt.Fprintf(w, "  echo \"Unable to continue!\"\n")
	fmt.Fprintf(w, "  exit 1\n")
	fmt.Fprintf(w, "}\n")
	fmt.Fprintf(w, "trap handledTrouble INT TERM\n")
	p.PrintNormal(w)
	fmt.Fprintf(w, "%s\n", hereDocName)
}

// check reports the error fatally if it's non-nil.
func check(msg string, err error) {
	if err != nil {
		fmt.Printf("Problem with %s: %v\n", msg, err)
		glog.Fatal(err)
	}
}

// accumulateOutput returns a channel to which it writes objects that
// contain what purport to be the entire output of one command block.
//
// To do so, it accumulates strings off a channel representing command
// block output until the channel closes, or until a string arrives
// that matches a particular pattern.
//
// On the happy path, strings are accumulated and every so often sent
// out with a success == true flag attached.  This continues until the
// input channel closes.
//
// On a sad path, an accumulation of strings is sent with a success ==
// false flag attached, and the function exits early, before it's
// input channel closes.
func accumulateOutput(prefix string, in <-chan string) <-chan *model.BlockOutput {
	out := make(chan *model.BlockOutput)
	var accum bytes.Buffer
	go func() {
		defer close(out)
		for line := range in {
			if strings.HasPrefix(line, scanner.MsgTimeout) {
				accum.WriteString("\n" + line + "\n")
				accum.WriteString("A subprocess might still be running.\n")
				if glog.V(2) {
					glog.Info("accumulateOutput %s: Timeout return.", prefix)
				}
				out <- model.NewFailureOutput(accum.String())
				return
			}
			if strings.HasPrefix(line, scanner.MsgError) {
				accum.WriteString(line + "\n")
				if glog.V(2) {
					glog.Info("accumulateOutput %s: Error return.", prefix)
				}
				out <- model.NewFailureOutput(accum.String())
				return
			}
			if strings.HasPrefix(line, scanner.MsgHappy) {
				if glog.V(2) {
					glog.Info("accumulateOutput %s: %s", prefix, line)
				}
				out <- model.NewSuccessOutput(accum.String())
				accum.Reset()
			} else {
				if glog.V(2) {
					glog.Info("accumulateOutput %s: Accumulating [%s]", prefix, line)
				}
				accum.WriteString(line + "\n")
			}
		}

		if glog.V(2) {
			glog.Info("accumulateOutput %s: <--- This channel has closed.", prefix)
		}
		trailing := strings.TrimSpace(accum.String())
		if len(trailing) > 0 {
			if glog.V(2) {
				glog.Info(
					"accumulateOutput %s: Erroneous (missing-happy) output [%s]",
					prefix, accum.String())
			}
			out <- model.NewFailureOutput(accum.String())
		} else {
			if glog.V(2) {
				glog.Info("accumulateOutput %s: Nothing trailing.", prefix)
			}
		}
	}()
	return out
}

// userBehavior acts like a command line user.
//
// TODO(monopole): update the comments, as this function no longer writes to stdin.
// See https://github.com/monopole/mdrip/commit/a7be6a6fb62ccf8dfe1c2906515ce3e83d0400d7
//
// It writes command blocks to shell, then waits after  each block to
// see if the block worked.  If the block appeared to complete without
// error, the routine sends the next block, else it exits early.
func (p *Program) userBehavior(stdOut, stdErr io.ReadCloser) (errResult *model.RunResult) {

	chOut := scanner.BuffScanner(p.blockTimeout, "stdout", stdOut)
	chErr := scanner.BuffScanner(1*time.Minute, "stderr", stdErr)

	chAccOut := accumulateOutput("stdOut", chOut)
	chAccErr := accumulateOutput("stdErr", chErr)

	errResult = model.NewRunResult()
	for _, script := range p.Scripts {
		numBlocks := len(script.Blocks())
		for i, block := range script.Blocks() {
			glog.Info("Running %s (%d/%d) from %s\n",
				block.Name(), i+1, numBlocks, script.FileName())
			if glog.V(2) {
				glog.Info("userBehavior: sending \"%s\"", block.Code())
			}

			result := <-chAccOut

			if result == nil || !result.Succeeded() {
				// A nil result means stdout has closed early because a
				// sub-subprocess failed.
				if result == nil {
					if glog.V(2) {
						glog.Info("userBehavior: stdout Result == nil.")
					}
					// Perhaps chErr <- scanner.MsgError +
					//   " : early termination; stdout has closed."
				} else {
					if glog.V(2) {
						glog.Info("userBehavior: stdout Result: %s", result.Output())
					}
					errResult.SetOutput(result.Output()).SetMessage(result.Output())
				}
				errResult.SetFileName(script.FileName()).SetIndex(i).SetBlock(block)
				fillErrResult(chAccErr, errResult)
				return
			}
		}
	}
	glog.Info("All done, no errors triggered.\n")
	return
}

// fillErrResult fills an instance of RunResult.
func fillErrResult(chAccErr <-chan *model.BlockOutput, errResult *model.RunResult) {
	result := <-chAccErr
	if result == nil {
		if glog.V(2) {
			glog.Info("userBehavior: stderr Result == nil.")
		}
		errResult.SetProblem(errors.New("unknown"))
		return
	}
	errResult.SetProblem(errors.New(result.Output())).SetMessage(result.Output())
	if glog.V(2) {
		glog.Info("userBehavior: stderr Result: %s", result.Output())
	}
}

// RunInSubShell runs command blocks in a subprocess, stopping and
// reporting on any error.  The subprocess runs with the -e flag, so
// it will abort if any sub-subprocess (any command) fails.
//
// Command blocks are strings presumably holding code from some shell
// language.  The strings may be more complex than single commands
// delimitted by linefeeds - e.g. blocks that operate on HERE
// documents, or multi-line commands using line continuation via '\',
// quotes or curly brackets.
//
// This function itself is not a shell interpreter, so it has no idea
// if one line of text from a command block is an individual command
// or part of something else.
//
// Error reporting works by discarding output from command blocks that
// succeeded, and only reporting the contents of stdout and stderr
// when the subprocess exits on error.
func (p *Program) RunInSubShell() (result *model.RunResult) {
	// Write program to a file to be executed.
	tmpFile, err := ioutil.TempFile("", "mdrip-script-")
	check("create temp file", err)
	check("chmod temp file", os.Chmod(tmpFile.Name(), 0744))
	for _, script := range p.Scripts {
		for _, block := range script.Blocks() {
			write(tmpFile, block.Code().String())
			write(tmpFile, "\n")
			write(tmpFile, "echo "+scanner.MsgHappy+" "+block.Name().String()+"\n")
		}
	}
	if glog.V(2) {
		glog.Info("RunInSubShell: running commands from %s", tmpFile.Name())
	}
	defer func() {
		check("delete temp file", os.Remove(tmpFile.Name()))
	}()

	// Adding "-e" to force the subshell to die on any error.
	shell := exec.Command("bash", "-e", tmpFile.Name())

	stdIn, err := shell.StdinPipe()
	check("in pipe", err)
	check("close shell's stdin", stdIn.Close())

	stdOut, err := shell.StdoutPipe()
	check("out pipe", err)

	stdErr, err := shell.StderrPipe()
	check("err pipe", err)

	err = shell.Start()
	check("shell start", err)

	pid := shell.Process.Pid
	if glog.V(2) {
		glog.Info("RunInSubShell: pid = %d", pid)
	}
	pgid, err := util.GetProcesssGroupId(pid)
	if err == nil {
		if glog.V(2) {
			glog.Info("RunInSubShell:  pgid = %d", pgid)
		}
	}

	result = p.userBehavior(stdOut, stdErr)

	if glog.V(2) {
		glog.Info("RunInSubShell:  Waiting for shell to end.")
	}
	waitError := shell.Wait()
	if result.Problem() == nil {
		result.SetProblem(waitError)
	}
	if glog.V(2) {
		glog.Info("RunInSubShell:  Shell done.")
	}

	// killProcesssGroup(pgid)
	return
}

func write(writer io.Writer, output string) {
	n, err := writer.Write([]byte(output))
	if err != nil {
		glog.Fatalf("Could not write %d bytes: %v", len(output), err)
	}
	if n != len(output) {
		glog.Fatalf("Expected to write %d bytes, wrote %d", len(output), n)
	}
}

// Serve offers an http service.
// A handler writes command blocks to an executor for execution.
func (p *Program) Serve(executor io.Writer, hostAndPort string) {
	http.HandleFunc("/", p.showControlPage)
	http.HandleFunc("/favicon.ico", p.favicon)
	http.HandleFunc("/image", p.image)
	http.HandleFunc("/runblock", p.makeBlockRunner(executor))
	http.HandleFunc("/q", p.quit)
	fmt.Println("Serving at http://" + hostAndPort)
	fmt.Println()
	glog.Info("Serving at " + hostAndPort)
	glog.Fatal(http.ListenAndServe(hostAndPort, nil))
}

func (p *Program) favicon(w http.ResponseWriter, r *http.Request) {
	model.Lissajous(w, 7, 3, 1)
}

func (p *Program) image(w http.ResponseWriter, r *http.Request) {
	model.Lissajous(w,
		getIntParam("s", r, 300),
		getIntParam("c", r, 30),
		getIntParam("n", r, 100))
}

func getIntParam(n string, r *http.Request, d int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(n))
	if err != nil {
		return d
	}
	return v
}

func (p *Program) quit(w http.ResponseWriter, r *http.Request) {
	os.Exit(0)
}

const headerHtml = `
<head>
<style type="text/css">
body {
  background-color: antiquewhite;
}

div.commandBlock {
  /* background-color: red; */
  margin: 0px;
  border: 0px;
  padding: 0px;
}

.control {
  font-family: "Times New Roman", Times, sans-serif;
  font-size: 1.4em;
  font-weight: bold;
  font-style: oblique;
  margin: 15px 10px 12px 0px;
  border: 0px;
  padding: 0px;
}

.blockButton {
  height: 100%;
  cursor: pointer;
}

.spacer {
  height: 100%;
  width: 5px;
}

pre.codeblock {
  font-family: "Lucida Console", Monaco, monospace;
  font-size: 0.8em;
  color: #33ff66;
  background-color: black;
  /* top rig bot lef */
  padding: 10px 20px 0px 20px;
  margin: 0px;
  border: 0px;
}

.didit {
  display: inline-block;
  width: 24px;
  height: 20px;
  background-repeat: no-repeat;
  background-size: contain;
  background-image: url(data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABgAAAAWCAMAAADto6y6AAAABGdBTUEAALGPC/xhBQAAAAFzUkdCAK7OHOkAAAAgY0hSTQAAeiYAAICEAAD6AAAAgOgAAHUwAADqYAAAOpgAABdwnLpRPAAAAQtQTFRFAAAAAH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//AH//////BQzC2AAAAFd0Uk5TAAADLy4QZVEHKp8FAUnHbeJ3BAh68IYGC4f4nQyM/LkYCYnXf/rvAm/2/oFY7rcTPuHkOCEky3YjlW4Pqbww0MVTfUZA96p061Xs3mz1e4P70R2aHJYf2KM0AgAAAAFiS0dEWO21xI4AAAAJcEhZcwAAEysAABMrAbkohUIAAADTSURBVCjPbdDZUsJAEAXQXAgJIUDCogHBkbhFEIgCsqmo4MImgij9/39iUT4Qkp63OV0zfbsliTkIhWWOEVHUKOdaTNER9HgiaYQY1xUzlWY8kz04tBjP5Y8KRc6PxUmJcftUnMkIFGCdX1yqjDtX5cp1MChQrVHd3Xn8/y1wc0uNpuejZmt7Ae7aJDreBt1e3wVw/0D06HobYPD0/GI7Q0G10V4i4NV8e/8YE/V8KwImUxJEM82fFM78k4gW3MhfS1p9B3ckobgWBpiChJ/fjc//AJIfFr4X0swAAAAAJXRFWHRkYXRlOmNyZWF0ZQAyMDE2LTA3LTMwVDE0OjI3OjUxLTA3OjAwUzMirAAAACV0RVh0ZGF0ZTptb2RpZnkAMjAxNi0wNy0zMFQxNDoyNzo0NC0wNzowMLz8tSkAAAAZdEVYdFNvZnR3YXJlAHd3dy5pbmtzY2FwZS5vcmeb7jwaAAAAFXRFWHRUaXRsZQBibHVlIENoZWNrIG1hcmsiA8jIAAAAAElFTkSuQmCC);
}
</style>
<script type="text/javascript">
  // blockUx, which may cause screen flicker, not needed if write is very fast.
  var blockUx = false
  var runButtons = []
  var requestRunning = false
  function onLoad() {
    if (blockUx) {
      runButtons = document.getElementsByTagName('input');
    }
  }
  function getId(el) {
    return el.getAttribute("data-id");
  }
  function setRunButtonsDisabled(value) {
    for (var i = 0; i < runButtons.length; i++) {
      runButtons[i].disabled = value;
    }
  }
  function addCheck(el) {
    var t = 'span';
    var c = document.createElement(t);
    c.setAttribute('class', 'didit');        
    el.appendChild(c);
  }
  function onRunBlockClick(event) {
    if (!(event && event.target)) {
      alert('no event!');
      return
    }
    if (requestRunning) {
      alert('busy!');
      return
    }
    requestRunning = true;
    if (blockUx) {
      setRunButtonsDisabled(true)
    }
    var b = event.target;
    blockId = getId(b.parentNode.parentNode);
    scriptId = getId(b.parentNode.parentNode.parentNode);
    var oldColor = b.style.color;
    var oldValue = b.value;
    if (blockUx) {
       b.style.color = 'red';
       b.value = 'running...';
    }
    var xhttp = new XMLHttpRequest();
    xhttp.onreadystatechange = function() {
      if (xhttp.readyState == XMLHttpRequest.DONE) {
        if (blockUx) {
          b.style.color = oldColor;
          b.value = oldValue;
        }
        addCheck(b.parentNode)
        requestRunning = false;
        if (blockUx) {
          setRunButtonsDisabled(false);
        }
      }
    };
    xhttp.open('GET', '/runblock?sid=' + scriptId + '&bid=' + blockId, true);
    xhttp.send();
  }
</script>
</head>
`

func (p *Program) makeBlockRunner(executor io.Writer) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO(jregan): 404 on bad params
		indexScript := getIntParam("sid", r, -1)
		indexBlock := getIntParam("bid", r, -1)
		block := p.Scripts[indexScript].Blocks()[indexBlock]

		glog.Info("Running ", block.Name())
		_, err := executor.Write(block.Code().Bytes())

		if err != nil {
			fmt.Fprintln(w, err)
			return
		}
		fmt.Fprintln(w, "Ok")
	}
}

func (p *Program) showControlPage(w http.ResponseWriter, r *http.Request) {
	p.Reload()
	fmt.Fprintln(w, `<html>`+headerHtml+`<body onload="onLoad()">`)
	if err := templates.ExecuteTemplate(w, tmplNameProgram, p); err != nil {
		glog.Fatal(err)
	}
	fmt.Fprintln(w, `</body></html>`)
}
