// Code generated by Wire. DO NOT EDIT.

//go:generate wire
//+build !wireinject

package main

// Injectors from wire.go:

func injectedMessage() string {
	s := provideS()
	string2 := s.Foo
	return string2
}

func injectedMessagePtr() *string {
	s := provideS()
	string2 := &s.Foo
	return string2
}
