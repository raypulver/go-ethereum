package jsre

import (
	"fmt"
	"io/ioutil"
	"sync"
	"time"
        "os"
        "os/exec"
        "path"
	"github.com/ethereum/go-ethereum/common"
	"github.com/robertkrimen/otto"
)

/*
JSRE is a generic JS runtime environment embedding the otto JS interpreter.
It provides some helper functions to
- load code from files
- run code snippets
- require libraries
- bind native go objects
*/
type JSRE struct {
	assetPath     string
	evalQueue     chan *evalReq
	stopEventLoop chan bool
	loopWg        sync.WaitGroup
}

// jsTimer is a single timer instance with a callback function
type jsTimer struct {
	timer    *time.Timer
	duration time.Duration
	interval bool
	call     otto.FunctionCall
}

// evalReq is a serialized vm execution request processed by runEventLoop.
type evalReq struct {
	fn   func(vm *otto.Otto)
	done chan bool
}

// runtime must be stopped with Stop() after use and cannot be used after stopping
func New(assetPath string) *JSRE {
	re := &JSRE{
		assetPath:     assetPath,
		evalQueue:     make(chan *evalReq),
		stopEventLoop: make(chan bool),
	}
	re.loopWg.Add(1)
	go re.runEventLoop()
	re.Compile("pp.js", pp_js) // load prettyprint func definition
	re.Set("loadScript", re.loadScript)
        re.Set("fs", struct{}{})
        t, _ := re.Get("fs")
        fs := t.Object()
        re.Set("process", struct{}{})
        t, _ = re.Get("process")
        processObj := t.Object()
        fs.Set("readFile", re.readFile)
        fs.Set("writeFile", re.writeFile)
        fs.Set("appendFile", re.appendFile)
        fs.Set("readdir", re.readdir)
        processObj.Set("env", os.Environ())
        processObj.Set("argv", os.Args)
        processObj.Set("chdir", re.chdir)
        processObj.Set("exec", re.shellExec)
        rcpath := path.Join(os.Getenv(`HOME`), `.gethrc`)
        if _, err := os.Stat(rcpath); err == nil {
          re.Run(`
            loadScript('` + rcpath + `');
          `)
        }
        re.Run(`
          process.env = (function (env) {
            var ret = {}, re = /(^.*?)=(.*$)/;
            env.forEach(function (v) {
              var parts = re.exec(v);
              ret[parts[1]] = parts[2]
            });
            return ret;
          })(process.env);
        `)
	return re
}

// This function runs the main event loop from a goroutine that is started
// when JSRE is created. Use Stop() before exiting to properly stop it.
// The event loop processes vm access requests from the evalQueue in a
// serialized way and calls timer callback functions at the appropriate time.

// Exported functions always access the vm through the event queue. You can
// call the functions of the otto vm directly to circumvent the queue. These
// functions should be used if and only if running a routine that was already
// called from JS through an RPC call.
func (self *JSRE) runEventLoop() {
	vm := otto.New()
	registry := map[*jsTimer]*jsTimer{}
	ready := make(chan *jsTimer)

	newTimer := func(call otto.FunctionCall, interval bool) (*jsTimer, otto.Value) {

		delay, _ := call.Argument(1).ToInteger()
		if 0 >= delay {
			delay = 1
		}
		timer := &jsTimer{
			duration: time.Duration(delay) * time.Millisecond,
			call:     call,
			interval: interval,
		}
		registry[timer] = timer

		timer.timer = time.AfterFunc(timer.duration, func() {
			ready <- timer
		})

		value, err := call.Otto.ToValue(timer)
		if err != nil {
			panic(err)
		}

		return timer, value
	}

	setTimeout := func(call otto.FunctionCall) otto.Value {
		_, value := newTimer(call, false)
		return value
	}

	setInterval := func(call otto.FunctionCall) otto.Value {
		_, value := newTimer(call, true)
		return value
	}

	clearTimeout := func(call otto.FunctionCall) otto.Value {
		timer, _ := call.Argument(0).Export()
		if timer, ok := timer.(*jsTimer); ok {
			timer.timer.Stop()
			delete(registry, timer)
		}
		return otto.UndefinedValue()
	}
	vm.Set("setTimeout", setTimeout)
	vm.Set("setInterval", setInterval)
	vm.Set("clearTimeout", clearTimeout)
	vm.Set("clearInterval", clearTimeout)

	var waitForCallbacks bool

loop:
	for {
		select {
		case timer := <-ready:
			// execute callback, remove/reschedule the timer
			var arguments []interface{}
			if len(timer.call.ArgumentList) > 2 {
				tmp := timer.call.ArgumentList[2:]
				arguments = make([]interface{}, 2+len(tmp))
				for i, value := range tmp {
					arguments[i+2] = value
				}
			} else {
				arguments = make([]interface{}, 1)
			}
			arguments[0] = timer.call.ArgumentList[0]
			_, err := vm.Call(`Function.call.call`, nil, arguments...)
			if err != nil {
				fmt.Println("js error:", err, arguments)
			}
			if timer.interval {
				timer.timer.Reset(timer.duration)
			} else {
				delete(registry, timer)
				if waitForCallbacks && (len(registry) == 0) {
					break loop
				}
			}
		case req := <-self.evalQueue:
			// run the code, send the result back
			req.fn(vm)
			close(req.done)
			if waitForCallbacks && (len(registry) == 0) {
				break loop
			}
		case waitForCallbacks = <-self.stopEventLoop:
			if !waitForCallbacks || (len(registry) == 0) {
				break loop
			}
		}
	}

	for _, timer := range registry {
		timer.timer.Stop()
		delete(registry, timer)
	}

	self.loopWg.Done()
}

// do schedules the given function on the event loop.
func (self *JSRE) do(fn func(*otto.Otto)) {
	done := make(chan bool)
	req := &evalReq{fn, done}
	self.evalQueue <- req
	<-done
}

// stops the event loop before exit, optionally waits for all timers to expire
func (self *JSRE) Stop(waitForCallbacks bool) {
	self.stopEventLoop <- waitForCallbacks
	self.loopWg.Wait()
}

// Exec(file) loads and runs the contents of a file
// if a relative path is given, the jsre's assetPath is used
func (self *JSRE) Exec(file string) error {
	code, err := ioutil.ReadFile(common.AbsolutePath(self.assetPath, file))
	if err != nil {
		return err
	}
	self.do(func(vm *otto.Otto) { _, err = vm.Run(code) })
	return err
}

// Bind assigns value v to a variable in the JS environment
// This method is deprecated, use Set.
func (self *JSRE) Bind(name string, v interface{}) error {
	return self.Set(name, v)
}

// Run runs a piece of JS code.
func (self *JSRE) Run(code string) (v otto.Value, err error) {
	self.do(func(vm *otto.Otto) { v, err = vm.Run(code) })
	return v, err
}

// Get returns the value of a variable in the JS environment.
func (self *JSRE) Get(ns string) (v otto.Value, err error) {
	self.do(func(vm *otto.Otto) { v, err = vm.Get(ns) })
	return v, err
}

// Set assigns value v to a variable in the JS environment.
func (self *JSRE) Set(ns string, v interface{}) (err error) {
	self.do(func(vm *otto.Otto) { err = vm.Set(ns, v) })
	return err
}

// loadScript executes a JS script from inside the currently executing JS code.
func (self *JSRE) loadScript(call otto.FunctionCall) otto.Value {
	file, err := call.Argument(0).ToString()
	if err != nil {
		// TODO: throw exception
		return otto.FalseValue()
	}
	file = common.AbsolutePath(self.assetPath, file)
	source, err := ioutil.ReadFile(file)
	if err != nil {
		// TODO: throw exception
		return otto.FalseValue()
	}
	if _, err := compileAndRun(call.Otto, file, source); err != nil {
		// TODO: throw exception
		fmt.Println("err:", err)
		return otto.FalseValue()
	}
	// TODO: return evaluation result
	return otto.TrueValue()
}

func (self *JSRE) readFile(call otto.FunctionCall) otto.Value {
        file, err := call.Argument(0).ToString()
        contents, err := ioutil.ReadFile(file)
        if err != nil {
            return otto.FalseValue()
        }
        value, _ := otto.ToValue(string(contents))
        return value
}
func (self *JSRE) writeFile(call otto.FunctionCall) otto.Value {
        file, err := call.Argument(0).ToString()
        if err != nil { return otto.FalseValue() }
        content, err  := call.Argument(1).ToString()
        if err != nil { return otto.FalseValue() }
        err = ioutil.WriteFile(file, []byte(content), 0644);
        if err != nil { return otto.FalseValue() }
        return otto.TrueValue()
}
func (self *JSRE) appendFile(call otto.FunctionCall) otto.Value {
        fn, err := call.Argument(0).ToString()
        if err != nil { return otto.FalseValue() }
        content, err  := call.Argument(1).ToString()
        if err != nil { return otto.FalseValue() }
        var file *os.File
        if _, err = os.Stat(fn); os.IsNotExist(err) {
                file, err = os.Create(fn)
        } else {
                file, err = os.OpenFile(fn, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0644)
        }
        _, err = file.Write([]byte(content))
        if err != nil { return otto.FalseValue() }
        err = file.Close()
        if err != nil { return otto.FalseValue() }
        return otto.TrueValue()
}
func (self *JSRE) readdir(call otto.FunctionCall) otto.Value {
        path, err := call.Argument(0).ToString()
        if err != nil { return otto.FalseValue() }
        files, err := ioutil.ReadDir(path)
        if err != nil { return otto.FalseValue() }
        filenames := make([]string, len(files))
        for i, element := range files {
                filenames[i] = element.Name()
        }
        ret, _ := call.Otto.ToValue(filenames)
        return ret
}
func (self *JSRE) chdir(call otto.FunctionCall) otto.Value {
        path, err := call.Argument(0).ToString()
        if err != nil { return otto.FalseValue() }
        err = os.Chdir(path)
        if err != nil { return otto.FalseValue() }
        return otto.TrueValue()
} 
func (self *JSRE) shellExec(call otto.FunctionCall) otto.Value {
        cmd, err := call.Argument(0).ToString()
        if err != nil { return otto.FalseValue(); }
        execution := exec.Command("sh", "-c", cmd)
        stdout, err := execution.CombinedOutput()
        var stderr string
        if err == nil {
            stderr = ""
        } else {
            stderr = err.Error()
        }
        t, _ := call.Otto.Call("new Object", nil)
        obj := t.Object()
        obj.Set("stdout", string(stdout))
        obj.Set("stderr", stderr)
        ret, _ := call.Otto.ToValue(obj)
        return ret 
}
// PrettyPrint writes v to standard output.
func (self *JSRE) PrettyPrint(v interface{}) (val otto.Value, err error) {
	var method otto.Value
	self.do(func(vm *otto.Otto) {
		val, err = vm.ToValue(v)
		if err != nil {
			return
		}
		method, err = vm.Get("prettyPrint")
		if err != nil {
			return
		}
		val, err = method.Call(method, val)
	})
	return val, err
}

// Eval evaluates JS function and returns result in a pretty printed string format.
func (self *JSRE) Eval(code string) (s string, err error) {
	var val otto.Value
	val, err = self.Run(code)
	if err != nil {
		return
	}
	val, err = self.PrettyPrint(val)
	if err != nil {
		return
	}
	return fmt.Sprintf("%v", val), nil
}

// Compile compiles and then runs a piece of JS code.
func (self *JSRE) Compile(filename string, src interface{}) (err error) {
	self.do(func(vm *otto.Otto) { _, err = compileAndRun(vm, filename, src) })
	return err
}

func compileAndRun(vm *otto.Otto, filename string, src interface{}) (otto.Value, error) {
	script, err := vm.Compile(filename, src)
	if err != nil {
		return otto.Value{}, err
	}
	return vm.Run(script)
}
