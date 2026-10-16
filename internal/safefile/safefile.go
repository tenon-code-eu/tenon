// Package safefile lit des fichiers réguliers de façon sûre (sans suivre de
// lien symbolique ni bloquer sur un FIFO).
package safefile

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
	"unicode/utf8"
)

// ReadRegular lit au plus max octets de path. ok=false si le fichier est
// absent, non régulier ou un lien symbolique. Au-delà de max, le contenu est
// tronqué à la dernière rune entière et truncated vaut true.
func ReadRegular(path string, max int) (data []byte, truncated bool, ok bool, err error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	if !fi.Mode().IsRegular() {
		return nil, false, false, nil
	}
	// O_NOFOLLOW referme la fenêtre entre Lstat et ouverture (remplacement
	// par un lien symbolique) ; O_NONBLOCK évite de bloquer sur un FIFO échangé.
	fd, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, fs.ErrNotExist) {
			return nil, false, false, nil
		}
		return nil, false, false, err
	}
	defer fd.Close()
	// On lit un octet de plus pour détecter le dépassement sans tout charger.
	data, err = io.ReadAll(io.LimitReader(fd, int64(max)+1))
	if err != nil {
		return nil, false, false, err
	}
	if len(data) > max {
		data = data[:max]
		// Une rune coupée occupe au plus 3 octets de queue : on retire la
		// rune incomplète finale, sans toucher aux runes entières.
		if i := lastRuneStart(data); i >= 0 && !utf8.FullRune(data[i:]) {
			data = data[:i]
		}
		truncated = true
	}
	return data, truncated, true, nil
}

// lastRuneStart renvoie l'index du dernier octet de début de rune, ou -1.
func lastRuneStart(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			return i
		}
	}
	return -1
}
