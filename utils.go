package main

import (
	"fmt"
	"net/http"
)

func failOnError(err error, msg string) {
    if err != nil {
        fmt.Printf("%s: %s", msg, err)
        panic(err)
    }
}

func failWithStatusCode(err error, msg string, w http.ResponseWriter, statusCode int) {
    failGracefully(err, msg)
    w.WriteHeader(statusCode)
    fmt.Fprintf(w, msg)
}

func failGracefully(err error, msg string) {
    if err != nil {
        fmt.Printf("%s: %s", msg, err)
    }
}

//  Stack implementation
type Stacker interface {
    Len() int
    Push(interface{})
    Pop() interface{}
    Peek() interface{}
}

type Stack struct {
    topPtr *stackElement
    size   int
}

type stackElement struct {
    value interface{}
    next  *stackElement
}

func (s Stack) Len() int {
    return s.size
}

func (s *Stack) Push(v interface{}) {
    s.topPtr = &stackElement{
        value: v,
        next:  s.topPtr,
    }
    s.size++
}

func (s *Stack) Pop() interface{} {
    if s.size > 0 {
        retVal := s.topPtr.value
        s.topPtr = s.topPtr.next
        s.size--
        return retVal
    }
    return nil
}

func (s Stack) Peek() interface{} {
    if s.size > 0 {
        return s.topPtr.value
    }
    return nil
}
