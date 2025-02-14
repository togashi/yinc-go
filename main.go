package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/ghodss/yaml"
)

const (
	VERSION = "yinc version 0.3.0"
)

var CLI struct {
	IndentWidth          int              `help:"Indent width." short:"w" default:"2"`
	OutputMultiDocuments bool             `help:"Output multiple documents." short:"m"`
	IncludeTag           string           `help:"Specify include tag." default:"!include"`
	ReplaceTag           string           `help:"Specify replace tag." default:"!replace"`
	Version              kong.VersionFlag `help:"Show version." short:"V"`
	Files                []string         `help:"Files to process." arg:"" type:"path" optional:""`
}

type CDir struct {
	origin string
}

func ChDir(to string) *CDir {
	origin, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(to); err != nil {
		panic(err)
	}
	return &CDir{
		origin: origin,
	}
}

func (cd *CDir) Return() {
	if err := os.Chdir(cd.origin); err != nil {
		panic(err)
	}
}

type LineElements struct {
	pattern  *regexp.Regexp
	submatch struct {
		indent string
		text   string
		tag    string
		spec   string
	}
}

func NewLine(includeTag string, replaceTag string) *LineElements {
	tags := strings.Replace(fmt.Sprintf("(%s|%s)", includeTag, replaceTag), "!", "\\!", -1)
	l := &LineElements{}
	l.pattern = regexp.MustCompile(`^(?P<indent>\s*)((?P<text>[^\s#]+)\s+)?(?<tag>` + tags + `)\s+(?P<spec>.+)$`)
	return l
}

func (l *LineElements) Match(line []byte) bool {
	match := l.pattern.FindSubmatch(line)
	if match == nil {
		return false
	}
	for i, name := range l.pattern.SubexpNames() {
		if i != 0 && name != "" && i < len(match) {
			value := string(match[i])
			switch name {
			case "indent":
				l.submatch.indent = value
			case "text":
				l.submatch.text = value
			case "tag":
				l.submatch.tag = value
			case "spec":
				l.submatch.spec = value
			}
		}
	}
	return true
}

type SourceStream struct {
	Spec        string
	Indent      []byte
	FirstIndent []byte
	Writer      io.Writer
	parent      *SourceStream
	out         int64
	cdir        *CDir
}

func (s *SourceStream) WriteIndent(data ...[]byte) (n int, err error) {
	if s.out == 0 && s.FirstIndent != nil {
		n, err = s.Write(s.FirstIndent)
	} else {
		n, err = s.Write(s.Indent)
	}
	if err != nil {
		return 0, err
	}
	for _, d := range data {
		np, err := s.Write(d)
		if err != nil {
			return n, err
		}
		n += np
	}
	return n, nil
}

func (s *SourceStream) Write(data []byte) (n int, err error) {
	n, err = s.Writer.Write(data)
	s.out += int64(n)
	return n, err
}

func getCmdOutput(cmd string) (output []byte, err error) {
	var shell string
	var flag string
	if runtime.GOOS == "windows" {
		shell = "cmd"
		flag = "/c"
	} else {
		shell = "sh"
		flag = "-c"
	}
	output, err = exec.Command(shell, flag, cmd).Output()
	if err != nil {
		return nil, err
	}
	return output, nil
}

func (s *SourceStream) getReader() io.Reader {
	if s.Spec == "" || s.Spec == "-" {
		return io.Reader(os.Stdin)
	} else if strings.HasPrefix(s.Spec, "$(shell ") && strings.HasSuffix(s.Spec, ")") {
		cmdline := strings.TrimPrefix(s.Spec, "$(shell ")
		cmdline = strings.TrimSuffix(cmdline, ")")
		output, err := getCmdOutput(cmdline)
		if err != nil {
			panic(err)
		}
		s.Spec = ""
		return bytes.NewReader(output)
	} else if strings.HasPrefix(s.Spec, "$(json ") || strings.HasPrefix(s.Spec, ")") {
		jsonfile := strings.TrimPrefix(s.Spec, "$(json ")
		jsonfile = strings.TrimSuffix(jsonfile, ")")
		jsonBytes, err := os.ReadFile(jsonfile)
		if err != nil {
			panic(err)
		}
		yamlBytes, err := yaml.JSONToYAML(jsonBytes)
		if err != nil {
			panic(err)
		}
		s.cdir = ChDir(filepath.Dir(jsonfile))
		return bytes.NewReader(yamlBytes)
	} else if strings.HasPrefix(s.Spec, "http://") || strings.HasPrefix(s.Spec, "https://") {
		resp, err := http.Get(s.Spec)
		if err != nil {
			panic(err)
		}
		return resp.Body
	} else {
		file, err := os.Open(s.Spec)
		if err != nil {
			panic(err)
		}
		s.cdir = ChDir(filepath.Dir(s.Spec))
		return file
	}
}

func (s *SourceStream) Process() {
	bufReader := bufio.NewReaderSize(s.getReader(), 4096)
	if s.cdir != nil {
		defer s.cdir.Return()
	}
	lineElements := NewLine(CLI.IncludeTag, CLI.ReplaceTag)
	for {
		line, _, err := bufReader.ReadLine()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		if lineElements.Match(line) {
			newIndent := string(s.Indent) + lineElements.submatch.indent
			files, err := doublestar.FilepathGlob(lineElements.submatch.spec)
			if err != nil {
				panic(err)
			}
			for _, file := range files {
				var firstIndent string
				var indent = string(newIndent)
				if lineElements.submatch.text != "" && lineElements.submatch.tag == CLI.IncludeTag {
					s.WriteIndent([]byte(lineElements.submatch.indent + lineElements.submatch.text))
					if lineElements.submatch.text != "-" {
						s.Writer.Write([]byte("\n"))
					}
					indent += strings.Repeat(" ", CLI.IndentWidth)
					if lineElements.submatch.text == "-" {
						firstIndent = " "
					}
				}
				sub := s.SubStream(file, indent, firstIndent)
				sub.Process()
			}
		} else {
			s.WriteIndent(line, []byte("\n"))
		}
	}
}

func NewStream(spec string, writer io.Writer) *SourceStream {
	return &SourceStream{
		Spec:   spec,
		Writer: writer,
	}
}

func (s *SourceStream) SubStream(spec string, indent string, firstIndent string) *SourceStream {
	p := s.parent
	for p != nil {
		if p.Spec == spec {
			panic("cyclic include detected")
		}
		p = p.parent
	}
	sub := NewStream(spec, s.Writer)
	sub.Indent = []byte(indent)
	if firstIndent != "" {
		sub.FirstIndent = []byte(firstIndent)
	}
	sub.parent = s
	return sub
}

func main() {
	kong.Parse(&CLI, kong.Vars{"version": VERSION})
	if len(CLI.Files) == 0 {
		CLI.Files = append(CLI.Files, "-")
	}
	for i, file := range CLI.Files {
		if CLI.OutputMultiDocuments && i > 0 {
			os.Stdout.WriteString("---\n")
		}
		stream := NewStream(file, os.Stdout)
		stream.Process()
	}
}
