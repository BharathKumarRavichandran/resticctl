package sqlitebackup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	modernsqlite "modernc.org/sqlite"
)

const (
	sqliteBusy   = 5
	sqliteLocked = 6
)

type onlineBackupper interface {
	NewBackup(string) (*modernsqlite.Backup, error)
}

func Create(ctx context.Context, sourcePath, destination string) (finalErr error) {
	info, err := os.Stat(sourcePath)
	if err != nil || !info.Mode().IsRegular() {
		if errors.Is(err, os.ErrNotExist) || err == nil {
			return fmt.Errorf("SQLite database not found: %s", sourcePath)
		}
		return fmt.Errorf("cannot inspect SQLite database %s: %w", sourcePath, err)
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("refusing to overwrite SQLite snapshot: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot inspect SQLite snapshot %s: %w", destination, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("cannot create SQLite snapshot directory: %w", err)
	}
	defer func() {
		if finalErr != nil {
			if removeErr := os.Remove(destination); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				finalErr = errors.Join(finalErr, fmt.Errorf("cannot remove incomplete SQLite snapshot %s: %w", destination, removeErr))
			}
		}
	}()

	sourceURI, err := sqliteURI(sourcePath, "mode=ro")
	if err != nil {
		return err
	}
	destinationURI, err := sqliteURI(destination, "")
	if err != nil {
		return err
	}
	source, err := sql.Open("sqlite", sourceURI)
	if err != nil {
		return fmt.Errorf("cannot open SQLite database %s: %w", sourcePath, err)
	}
	defer source.Close()
	source.SetMaxOpenConns(1)
	connection, err := source.Conn(ctx)
	if err != nil {
		return fmt.Errorf("cannot open SQLite database %s: %w", sourcePath, err)
	}
	defer connection.Close()
	if err := connection.Raw(func(driverConnection any) error {
		backupper, ok := driverConnection.(onlineBackupper)
		if !ok {
			return errors.New("SQLite driver does not support online backups")
		}
		backup, err := backupper.NewBackup(destinationURI)
		if err != nil {
			return err
		}
		stepErr := stepBackup(ctx, backup)
		finishErr := backup.Finish()
		if stepErr != nil {
			return stepErr
		}
		return finishErr
	}); err != nil {
		return fmt.Errorf("cannot snapshot SQLite database %s: %w", sourcePath, err)
	}

	snapshotURI, err := sqliteURI(destination, "mode=ro")
	if err != nil {
		return err
	}
	snapshot, err := sql.Open("sqlite", snapshotURI)
	if err != nil {
		return fmt.Errorf("cannot check SQLite snapshot %s: %w", destination, err)
	}
	defer snapshot.Close()
	rows, err := snapshot.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("cannot check SQLite snapshot %s: %w", destination, err)
	}
	defer rows.Close()
	var results []string
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return fmt.Errorf("cannot read SQLite integrity result: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("cannot read SQLite integrity result: %w", err)
	}
	if len(results) != 1 || results[0] != "ok" {
		return fmt.Errorf("SQLite integrity check failed for %s: %v", sourcePath, results)
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		return fmt.Errorf("cannot protect SQLite snapshot %s: %w", destination, err)
	}
	return nil
}

func stepBackup(ctx context.Context, backup *modernsqlite.Backup) error {
	for {
		more, err := backup.Step(256)
		if err == nil {
			if !more {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				continue
			}
		}
		coded, ok := err.(interface{ Code() int })
		if !ok || (coded.Code() != sqliteBusy && coded.Code() != sqliteLocked) {
			return err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func sqliteURI(path, rawQuery string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("cannot resolve SQLite path %s: %w", path, err)
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute), RawQuery: rawQuery}).String(), nil
}
