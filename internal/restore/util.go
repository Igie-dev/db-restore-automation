package restore

import "io"

func ioReader(value any) io.Reader {
	if value == nil {
		return nil
	}
	reader, _ := value.(io.Reader)
	return reader
}
