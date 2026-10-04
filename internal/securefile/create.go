package securefile

import (
	"errors"
	"os"
)

// WriteNew creates a private file without overwriting an existing path.
func WriteNew(path string, data []byte) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, file.Close())
		if err != nil {
			err = errors.Join(err, Remove(path))
		}
	}()
	if err := Protect(path); err != nil {
		return err
	}
	_, err = file.Write(data)
	return err
}
