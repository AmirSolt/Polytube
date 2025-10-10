package utils

import "os"

func GetFileSizeMB(path string) (float64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	sizeInBytes := info.Size()
	sizeInMB := float64(sizeInBytes) / (1024 * 1024)
	return sizeInMB, nil
}
