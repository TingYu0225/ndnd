package repo

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// TODO: consider using a database for better performance when the catalog grows large.
// TODO: consider the case where insert update a file that is already on another server
func (c *Catalog) updateFileServer(fileName string, owner string, server string) error {
	entries, err := c.readAllCatalogEntries(c.catalogPath)
	if err != nil {
		return err
	}

	updated := false
	for i := range entries {
		if entries[i].File == fileName && entries[i].Owner == owner {
			entries[i].Server = server
			updated = true
			break
		}
	}

	if !updated {
		entries = append(entries, catalogEntry{
			File:   fileName,
			Owner:  owner,
			Server: server,
		})
	}

	return c.writeAllCatalogEntries(c.catalogPath, entries)
}

func (c *Catalog) findFileInfo(fileName string, owner string) ([]catalogEntry, error) {
	fileName = strings.TrimSpace(filepath.Base(fileName))

	file, err := os.Open(c.catalogPath)
	if err != nil {
		return nil, fmt.Errorf("open catalog failed: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	matches := make([]catalogEntry, 0)

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read csv catalog failed: %w", err)
		}
		if len(record) < 3 {
			continue
		}

		catalogFile := strings.TrimSpace(record[0])
		catalogOwner := strings.TrimSpace(record[1])
		catalogServer := strings.TrimSpace(record[2])

		if catalogFile == "" || catalogOwner == "" || catalogServer == "" {
			continue
		}
		if strings.EqualFold(catalogFile, "file") && strings.EqualFold(catalogOwner, "owner") && strings.EqualFold(catalogServer, "server") {
			continue
		}

		if catalogFile == fileName && (catalogOwner == owner || owner == "") {
			matches = append(matches, catalogEntry{
				File:   catalogFile,
				Owner:  catalogOwner,
				Server: catalogServer,
			})
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("file not found in catalog")
	}
	return matches, nil
}

func (c *Catalog) deleteFileFromCatalog(fileName string, owner string, server string) error {
	fileName = strings.TrimSpace(filepath.Base(fileName))
	if fileName == "" {
		return fmt.Errorf("file name is empty")
	}

	entries, err := c.readAllCatalogEntries(c.catalogPath)
	if err != nil {
		return err
	}

	filtered := make([]catalogEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.File != fileName || entry.Owner != owner || entry.Server != server {
			filtered = append(filtered, entry)
		}
	}

	return c.writeAllCatalogEntries(c.catalogPath, filtered)
}

// get catalog info
func (c *Catalog) readAllCatalogEntries(catalogPath string) ([]catalogEntry, error) {
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
		if len(record) < 3 {
			continue
		}

		catalogFile := strings.TrimSpace(record[0])
		catalogOwner := strings.TrimSpace(record[1])
		catalogServer := strings.TrimSpace(record[2])

		if catalogFile == "" || catalogOwner == "" || catalogServer == "" {
			continue
		}
		if strings.EqualFold(catalogFile, "file") && strings.EqualFold(catalogOwner, "owner") && strings.EqualFold(catalogServer, "server") {
			continue
		}

		entries = append(entries, catalogEntry{
			File:   catalogFile,
			Owner:  catalogOwner,
			Server: catalogServer,
		})
	}
}

func (c *Catalog) writeAllCatalogEntries(catalogPath string, entries []catalogEntry) error {
	file, err := os.OpenFile(catalogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open catalog for write failed: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)

	if err := writer.Write([]string{"file", "owner", "server"}); err != nil {
		return fmt.Errorf("write csv header failed: %w", err)
	}

	for _, entry := range entries {
		if err := writer.Write([]string{entry.File, entry.Owner, entry.Server}); err != nil {
			return fmt.Errorf("write csv entry failed: %w", err)
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush csv catalog failed: %w", err)
	}

	return nil
}
