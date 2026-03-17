package repo

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type catalogEntry struct {
	File string
	User string
}

func updateFileOwner(catalogPath string, fileName string, user string) error {
	fileName = strings.TrimSpace(filepath.Base(fileName))
	user = strings.TrimSpace(user)

	entries, err := readAllCatalogEntries(catalogPath)
	if err != nil {
		return err
	}

	updated := false
	for i := range entries {
		if entries[i].File == fileName {
			entries[i].User = user
			updated = true
			break
		}
	}

	if !updated {
		entries = append(entries, catalogEntry{
			File: fileName,
			User: user,
		})
	}

	return writeAllCatalogEntries(catalogPath, entries)
}

func findFileOwner(catalogPath string, fileName string) (string, error) {
	fileName = strings.TrimSpace(filepath.Base(fileName))

	file, err := os.Open(catalogPath)
	if err != nil {
		return "", fmt.Errorf("open catalog failed: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)

	for {
		record, err := reader.Read()
		if err == io.EOF {

			return "", fmt.Errorf("file not found in catalog")
		}
		if err != nil {
			return "", fmt.Errorf("read csv catalog failed: %w", err)
		}
		if len(record) < 2 {
			continue
		}

		catalogFile := strings.TrimSpace(record[0])
		catalogUser := strings.TrimSpace(record[1])

		if catalogFile == "" || catalogUser == "" {
			continue
		}
		if strings.EqualFold(catalogFile, "file") && strings.EqualFold(catalogUser, "user") {
			continue
		}

		if catalogFile == fileName {
			return catalogUser, nil
		}
	}
}

func deleteFileFromCatalog(catalogPath string, fileName string) error {
	fileName = strings.TrimSpace(filepath.Base(fileName))
	if fileName == "" {
		return fmt.Errorf("file name is empty")
	}

	entries, err := readAllCatalogEntries(catalogPath)
	if err != nil {
		return err
	}

	filtered := make([]catalogEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.File != fileName {
			filtered = append(filtered, entry)
		}
	}

	return writeAllCatalogEntries(catalogPath, filtered)
}

// get catalog info
func readAllCatalogEntries(catalogPath string) ([]catalogEntry, error) {
	file, err := os.Open(catalogPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open catalog failed: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	var entries []catalogEntry

	for {
		record, err := reader.Read()
		if err == io.EOF {
			return entries, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read csv catalog failed: %w", err)
		}
		if len(record) < 2 {
			continue
		}

		fileName := strings.TrimSpace(record[0])
		user := strings.TrimSpace(record[1])

		if fileName == "" || user == "" {
			continue
		}
		if strings.EqualFold(fileName, "file") && strings.EqualFold(user, "user") {
			continue
		}

		entries = append(entries, catalogEntry{
			File: fileName,
			User: user,
		})
	}
}

func writeAllCatalogEntries(catalogPath string, entries []catalogEntry) error {
	file, err := os.OpenFile(catalogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open catalog for write failed: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)

	if err := writer.Write([]string{"file", "user"}); err != nil {
		return fmt.Errorf("write csv header failed: %w", err)
	}

	for _, entry := range entries {
		if err := writer.Write([]string{entry.File, entry.User}); err != nil {
			return fmt.Errorf("write csv entry failed: %w", err)
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush csv catalog failed: %w", err)
	}

	return nil
}
