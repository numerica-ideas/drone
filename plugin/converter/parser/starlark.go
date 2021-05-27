package parser

import (
	"bytes"
	"github.com/drone/drone/core"
	"github.com/drone/drone/handler/api/errors"
	"github.com/sirupsen/logrus"
	"go.starlark.net/starlark"
)

const (
	separator = "---"
	newline   = "\n"
)

// limits generated configuration file size.
const limit = 1000000

var (
	// ErrMainMissing indicates the starlark script is missing
	// the main method.
	ErrMainMissing = errors.New("starlark: missing main function")

	// ErrMainInvalid indicates the starlark script defines a
	// global variable named main, however, it is not callable.
	ErrMainInvalid = errors.New("starlark: main must be a function")

	// ErrMainReturn indicates the starlark script's main method
	// returns an invalid or unexpected type.
	ErrMainReturn = errors.New("starlark: main returns an invalid type")

	// ErrMaximumSize indicates the starlark script generated a
	// file that exceeds the maximum allowed file size.
	ErrMaximumSize = errors.New("starlark: maximum file size exceeded")

	// ErrCannotLoad indicates the starlark script is attempting to
	// load an external file which is currently restricted.
	ErrCannotLoad = errors.New("starlark: cannot load external scripts")
)

func ParseStarlark(req *core.ConvertArgs, template *core.Template, templateData map[string]interface{}) (file *string, err error) {
	thread := &starlark.Thread{
		Name: "drone",
		Load: noLoad,
		Print: func(_ *starlark.Thread, msg string) {
			logrus.WithFields(logrus.Fields{
				"namespace": req.Repo.Namespace,
				"name":      req.Repo.Name,
			}).Traceln(msg)
		},
	}
	var starlarkFile []byte
	var starlarkFileName string
	if template != nil {
		starlarkFile = template.Data
		starlarkFileName = template.Name
	} else {
		starlarkFile = []byte(req.Config.Data)
		starlarkFileName = req.Repo.Config
	}

	globals, err := starlark.ExecFile(thread, starlarkFileName, starlarkFile, nil)
	if err != nil {
		return nil, err
	}

	// find the main method in the starlark script and
	// cast to a callable type. If not callable the script
	// is invalid.
	mainVal, ok := globals["main"]
	if !ok {
		return nil, ErrMainMissing
	}
	main, ok := mainVal.(starlark.Callable)
	if !ok {
		return nil, ErrMainInvalid
	}

	// create the input args and invoke the main method
	// using the input args.
	args := createArgs(req.Repo, req.Build, templateData)

	// set the maximum number of operations in the script. this
	// mitigates long running scripts.
	thread.SetMaxExecutionSteps(50000)

	// execute the main method in the script.
	mainVal, err = starlark.Call(thread, main, args, nil)
	if err != nil {
		return nil, err
	}

	buf := new(bytes.Buffer)
	switch v := mainVal.(type) {
	case *starlark.List:
		for i := 0; i < v.Len(); i++ {
			item := v.Index(i)
			buf.WriteString(separator)
			buf.WriteString(newline)
			if err := write(buf, item); err != nil {
				return nil, err
			}
			buf.WriteString(newline)
		}
	case *starlark.Dict:
		if err := write(buf, v); err != nil {
			return nil, err
		}
	default:
		return nil, ErrMainReturn
	}

	// this is a temporary workaround until we
	// implement a LimitWriter.
	if b := buf.Bytes(); len(b) > limit {
		return nil, nil
	}
	parsedFile := buf.String()
	return &parsedFile, nil
}

func noLoad(_ *starlark.Thread, _ string) (starlark.StringDict, error) {
	return nil, ErrCannotLoad
}