package main

type cfgFile struct {
	Size   uint64
	Block  uint64
	Chunk  uint64
	Cookie map[string]string
	Dir    string
}
