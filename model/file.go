package model
//go:generate msgp
type File struct {
	HostName string
	FileName string
	Content []byte
}
