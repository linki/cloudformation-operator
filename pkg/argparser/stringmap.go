package argparser

import (
  "fmt"
  "regexp"

  "github.com/alecthomas/kingpin"
)

// -- map[string]string Value
type stringMapValue map[string]string

func newStringMapValue(p *map[string]string) *stringMapValue {
  return (*stringMapValue)(p)
}

var stringMapRegex = regexp.MustCompile("[=]")

func (s *stringMapValue) Set(value string) error {
  parts := stringMapRegex.Split(value, 2)
  if len(parts) != 2 {
    return fmt.Errorf("expected KEY=VALUE got '%s'", value)
  }
  (*s)[parts[0]] = parts[1]
  return nil
}

func (s *stringMapValue) Get() interface{} {
  return (map[string]string)(*s)
}

func (s *stringMapValue) String() string {
  return fmt.Sprintf("%s", map[string]string(*s))
}

func (s *stringMapValue) IsCumulative() bool {
  return true
}

func StringMap(s kingpin.Settings) (target *map[string]string) {
  target = &map[string]string{}
  s.SetValue((*stringMapValue)(target))
  return
}
