package main

import (
	"flag"
	"fmt"
	"go/types"
	"os"
	"time"

	"github.com/google/pprof/profile"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

const Usage = `sprof: static profiling

Usage:

  sprof <package> <pprof output file>

  Package should be a main package.

`

//noinspection GoUnhandledErrorResult
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flag.Parse()

	if flag.NArg() != 2 {
		fmt.Fprintf(os.Stderr, Usage)
		flag.PrintDefaults()
		os.Exit(2)
	}

	fmt.Printf("analyzing source code ...\n")
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Tests: false,
		Dir:   "",
	}
	initial, err := packages.Load(cfg, flag.Arg(0))
	if err != nil {
		return err
	}

	if packages.PrintErrors(initial) > 0 {
		return fmt.Errorf("packages contain errors")
	}

	prog, pkgs := ssautil.AllPackages(initial, 0)
	prog.Build()

	mains, err := mainPackages(pkgs)
	if err != nil {
		return err
	}

	config := &pointer.Config{
		Mains:          mains,
		BuildCallGraph: true,
	}

	result, err := pointer.Analyze(config)
	if err != nil {
		return err
	}

	s := NewStack()
	samples := []Sample{}
	root := result.CallGraph.Root
	fmt.Printf("performing static profiling...\n")
	createStaticProfile(s, root, &samples)

	fmt.Printf("writing pprof file ...\n")
	functionID := uint64(1)
	locationID := uint64(1)

	p := &profile.Profile{
		TimeNanos: time.Now().UnixNano(),
	}
	m := &profile.Mapping{ID: 1, HasFunctions: true}
	p.Mapping = []*profile.Mapping{m}
	p.SampleType = []*profile.ValueType{
		{
			Type: "calls",
			Unit: "count",
		},
	}

	for _, s := range samples {
		sample := &profile.Sample{
			Value: []int64{s.Count},
		}

		for i := len(s.Frames) - 1; i >= 0; i-- {
			f := s.Frames[i]
			function := &profile.Function{
				ID:       functionID,
				Name:     f.String(),
				Filename: "main.go",
			}
			p.Function = append(p.Function, function)
			functionID++

			location := &profile.Location{
				ID:      locationID,
				Mapping: m,
				Line: []profile.Line{{
					Function: function,
					Line:     int64(1),
				}},
			}
			p.Location = append(p.Location, location)
			locationID++

			sample.Location = append(sample.Location, location)
		}

		p.Sample = append(p.Sample, sample)
	}

	outF, err := os.Create(flag.Arg(1))
	if err != nil {
		return err
	}

	if err := p.CheckValid(); err != nil {
		return err
	} else if err := p.Write(outF); err != nil {
		return err
	}
	return nil
}

type Sample struct {
	Frames []Func
	Count  int64
}

func NewStack() *Stack {
	return &Stack{seen: map[Func]struct{}{}}
}

type Stack struct {
	stack []Func
	seen  map[Func]struct{}
}

func (s *Stack) Add(fn Func) *Stack {
	seen := map[Func]struct{}{fn: {}}
	for fn := range s.seen {
		seen[fn] = struct{}{}
	}
	stack := make([]Func, len(s.stack)+1)
	copy(stack, s.stack)
	stack[len(stack)-1] = fn
	return &Stack{seen: seen, stack: stack}
}

func createStaticProfile(s *Stack, n *callgraph.Node, out *[]Sample) {
	var count int64
	if syn := n.Func.Syntax(); syn != nil {
		l := syn.End() - syn.Pos()
		count += int64(l) / int64(len(s.stack))
	}
	if len(n.Out) == 0 {
		count += 1
	}
	if count > 0 {
		*out = append(*out, Sample{Frames: s.stack, Count: count})
	}

	if len(s.stack) >= 8 {
		return
	}

	for _, node := range n.Out {
		fn := NewFunc(node.Callee)
		if fn.Name == "init" && fn.Receiver == "" {
			continue
		}

		// avoid recursion
		if _, ok := s.seen[fn]; ok {
			continue
		}
		ns := s.Add(fn)
		createStaticProfile(ns, node.Callee, out)
	}
}

type Func struct {
	PkgPath  string
	Receiver string
	Name     string
}

func (f Func) String() string {
	var s string
	if f.PkgPath != "" {
		s += f.PkgPath + "."
	}
	if f.Receiver != "" {
		s += "(" + f.Receiver + ")."
	}
	s += f.Name
	return s
}

func NewFunc(fn *callgraph.Node) (f Func) {
	f.Name = fn.Func.Name()
	if fn.Func.Pkg != nil {
		f.PkgPath = fn.Func.Pkg.Pkg.Path()
	}
	if r := fn.Func.Signature.Recv(); r != nil {
		switch t := r.Type().(type) {
		case *types.Pointer:
			f.Receiver += "*" + t.Elem().(*types.Named).Obj().Name()
		case *types.Named:
			f.Receiver += t.Obj().Name()
		}
	}
	return
}

// mainPackages returns the main packages to analyze.
// Each resulting package is named "main" and has a main function.
func mainPackages(pkgs []*ssa.Package) ([]*ssa.Package, error) {
	var mains []*ssa.Package
	for _, p := range pkgs {
		if p != nil && p.Pkg.Name() == "main" && p.Func("main") != nil {
			mains = append(mains, p)
		}
	}
	if len(mains) == 0 {
		return nil, fmt.Errorf("no main packages")
	}
	return mains, nil
}
