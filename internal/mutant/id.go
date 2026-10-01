package mutant

import (
	"fmt"
	"strconv"
	"strings"
)

type ID struct {
	File     string
	Function string
	Operator string
	Number   int
}

func ParseID(text string) (ID, error) {
	invalid := fmt.Errorf("%q is not a mutant id: an id is <file>:<function>:<operator>#<number>", text)
	place, number, found := cutLast(text, "#")
	n, err := strconv.Atoi(number)
	if !found || err != nil || n < 1 {
		return ID{}, invalid
	}
	fileAndFunction, operator, found := cutLast(place, ":")
	if !found || operator == "" {
		return ID{}, invalid
	}
	file, function, found := cutLast(fileAndFunction, ":")
	if !found || file == "" {
		return ID{}, invalid
	}
	return ID{File: file, Function: function, Operator: operator, Number: n}, nil
}

func (id ID) String() string {
	return fmt.Sprintf("%s:%s:%s#%d", id.File, id.Function, id.Operator, id.Number)
}

// Counter must see the mutants in source order.
type Counter struct {
	counts map[ID]int
}

func (c *Counter) Next(file, function, operator string) ID {
	if c.counts == nil {
		c.counts = map[ID]int{}
	}
	place := ID{File: file, Function: function, Operator: operator}
	c.counts[place]++
	place.Number = c.counts[place]
	return place
}

func cutLast(text, separator string) (before, after string, found bool) {
	position := strings.LastIndex(text, separator)
	if position < 0 {
		return text, "", false
	}
	return text[:position], text[position+len(separator):], true
}
