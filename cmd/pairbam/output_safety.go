package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type filePathRole struct {
	label string
	path  string
}

type stagedArtifact struct {
	stagedPath   string
	targetPath   string
	removeTarget bool
}

type artifactBackup struct {
	targetPath string
	backupPath string
	existed    bool
}

var renameArtifact = os.Rename

func validateDistinctFilePaths(pathRoles []filePathRole) error {
	canonicalPaths := make([]string, len(pathRoles))
	fileInfos := make([]os.FileInfo, len(pathRoles))
	for pathIndex, pathRole := range pathRoles {
		if pathRole.path == "" {
			return fmt.Errorf("%s path cannot be empty", pathRole.label)
		}
		canonicalPath, fileInfo, err := canonicalFileIdentity(pathRole.path)
		if err != nil {
			return fmt.Errorf("resolve %s path %q: %w", pathRole.label, pathRole.path, err)
		}
		canonicalPaths[pathIndex] = canonicalPath
		fileInfos[pathIndex] = fileInfo
	}

	for leftIndex := 0; leftIndex < len(pathRoles); leftIndex++ {
		for rightIndex := leftIndex + 1; rightIndex < len(pathRoles); rightIndex++ {
			sameCanonicalPath := canonicalPaths[leftIndex] == canonicalPaths[rightIndex]
			sameExistingFile := fileInfos[leftIndex] != nil &&
				fileInfos[rightIndex] != nil &&
				os.SameFile(fileInfos[leftIndex], fileInfos[rightIndex])
			if sameCanonicalPath || sameExistingFile {
				return fmt.Errorf(
					"%s path %q and %s path %q refer to the same file",
					pathRoles[leftIndex].label,
					pathRoles[leftIndex].path,
					pathRoles[rightIndex].label,
					pathRoles[rightIndex].path,
				)
			}
		}
	}
	return nil
}

func canonicalFileIdentity(path string) (string, os.FileInfo, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	absolutePath = filepath.Clean(absolutePath)

	fileInfo, statErr := os.Stat(absolutePath)
	if statErr == nil {
		resolvedPath, resolveErr := filepath.EvalSymlinks(absolutePath)
		if resolveErr != nil {
			return "", nil, resolveErr
		}
		return filepath.Clean(resolvedPath), fileInfo, nil
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return "", nil, statErr
	}

	parentDirectory := filepath.Dir(absolutePath)
	resolvedParent, resolveErr := filepath.EvalSymlinks(parentDirectory)
	if resolveErr != nil {
		if errors.Is(resolveErr, os.ErrNotExist) {
			return absolutePath, nil, nil
		}
		return "", nil, resolveErr
	}
	return filepath.Join(resolvedParent, filepath.Base(absolutePath)), nil, nil
}

func createStagedArtifactPath(targetPath, suffix string) (string, error) {
	targetDirectory := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDirectory, 0o755); err != nil {
		return "", fmt.Errorf("create output directory %q: %w", targetDirectory, err)
	}
	stagedFile, err := os.CreateTemp(targetDirectory, "."+filepath.Base(targetPath)+".staged-*"+suffix)
	if err != nil {
		return "", fmt.Errorf("reserve staged path for %q: %w", targetPath, err)
	}
	stagedPath := stagedFile.Name()
	if err := stagedFile.Close(); err != nil {
		_ = os.Remove(stagedPath)
		return "", fmt.Errorf("close staged placeholder for %q: %w", targetPath, err)
	}
	if err := os.Remove(stagedPath); err != nil {
		return "", fmt.Errorf("prepare staged path for %q: %w", targetPath, err)
	}
	return stagedPath, nil
}

func publishArtifacts(artifacts []stagedArtifact) error {
	for _, artifact := range artifacts {
		if artifact.removeTarget {
			continue
		}
		if _, err := os.Stat(artifact.stagedPath); err != nil {
			return fmt.Errorf("staged artifact %q is unavailable: %w", artifact.stagedPath, err)
		}
	}

	backups := make([]artifactBackup, 0, len(artifacts))
	for _, artifact := range artifacts {
		backup, err := backupExistingArtifact(artifact.targetPath)
		if err != nil {
			restoreErr := restoreArtifactBackups(backups)
			return errors.Join(err, restoreErr)
		}
		backups = append(backups, backup)
	}

	publishedCount := 0
	for _, artifact := range artifacts {
		if artifact.removeTarget {
			publishedCount++
			continue
		}
		if err := renameArtifact(artifact.stagedPath, artifact.targetPath); err != nil {
			rollbackErrors := []error{fmt.Errorf("publish %q: %w", artifact.targetPath, err)}
			for publishedIndex := 0; publishedIndex < publishedCount; publishedIndex++ {
				if removeErr := os.Remove(artifacts[publishedIndex].targetPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					rollbackErrors = append(rollbackErrors, removeErr)
				}
			}
			if restoreErr := restoreArtifactBackups(backups); restoreErr != nil {
				rollbackErrors = append(rollbackErrors, restoreErr)
			}
			return errors.Join(rollbackErrors...)
		}
		publishedCount++
	}

	for _, backup := range backups {
		if backup.existed {
			_ = os.Remove(backup.backupPath)
		}
	}
	return nil
}

func backupExistingArtifact(targetPath string) (artifactBackup, error) {
	if _, err := os.Stat(targetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return artifactBackup{targetPath: targetPath}, nil
		}
		return artifactBackup{}, fmt.Errorf("inspect existing output %q: %w", targetPath, err)
	}

	backupFile, err := os.CreateTemp(filepath.Dir(targetPath), "."+filepath.Base(targetPath)+".backup-*")
	if err != nil {
		return artifactBackup{}, fmt.Errorf("reserve backup for %q: %w", targetPath, err)
	}
	backupPath := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		_ = os.Remove(backupPath)
		return artifactBackup{}, fmt.Errorf("close backup placeholder for %q: %w", targetPath, err)
	}
	if err := os.Remove(backupPath); err != nil {
		return artifactBackup{}, fmt.Errorf("prepare backup for %q: %w", targetPath, err)
	}
	if err := renameArtifact(targetPath, backupPath); err != nil {
		return artifactBackup{}, fmt.Errorf("back up existing output %q: %w", targetPath, err)
	}
	return artifactBackup{targetPath: targetPath, backupPath: backupPath, existed: true}, nil
}

func restoreArtifactBackups(backups []artifactBackup) error {
	var restoreErrors []error
	for backupIndex := len(backups) - 1; backupIndex >= 0; backupIndex-- {
		backup := backups[backupIndex]
		if !backup.existed {
			continue
		}
		if err := renameArtifact(backup.backupPath, backup.targetPath); err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("restore %q: %w", backup.targetPath, err))
		}
	}
	return errors.Join(restoreErrors...)
}
