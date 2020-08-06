package validator

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

const ItemTypeFile = "file"
const ItemTypeFolder = "folder"

type folderSpec struct {
	fs        http.FileSystem
	specPath  string
	itemSpecs []folderItemSpec
}

type folderItemSpec struct {
	Description      string           `yaml:"description"`
	ItemType         string           `yaml:"type"`
	ContentMediaType string           `yaml:"contentMediaType"`
	Name             string           `yaml:"name"`
	Pattern          string           `yaml:"pattern"`
	Required         bool             `yaml:"required"`
	Ref              string           `yaml:"$ref"`
	Contents         []folderItemSpec `yaml:"contents"`
}

func newFolderSpec(fs http.FileSystem, specPath string) (*folderSpec, error) {
	specFile, err := fs.Open(specPath)
	if err != nil {
		return nil, errors.Wrap(err, "could not open folder specification file")
	}
	defer specFile.Close()

	data, err := ioutil.ReadAll(specFile)
	if err != nil {
		return nil, errors.Wrap(err, "could not read folder specification file")
	}

	var wrapper struct {
		Spec []folderItemSpec `yaml:",flow"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		return nil, errors.Wrap(err, "could not parse folder specification file")
	}

	spec := folderSpec{
		fs,
		specPath,
		wrapper.Spec,
	}

	return &spec, nil
}

func (s *folderSpec) validate(folderPath string) ValidationErrors {
	var errs ValidationErrors
	files, err := ioutil.ReadDir(folderPath)
	if err != nil {
		errs = append(errs, errors.Wrapf(err, "could not read folder [%s]", folderPath))
		return errs
	}

	for _, file := range files {
		fileName := file.Name()
		itemSpec, err := s.findItemSpec(fileName)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if itemSpec == nil {
			errs = append(errs, fmt.Errorf("filename [%s] does not match spec for folder [%s]", fileName, folderPath))
			continue
		}

		if file.IsDir() {
			if !itemSpec.isSameType(file) {
				errs = append(errs, fmt.Errorf("[%s] is a folder but is expected to be a file", fileName))
				continue
			}

			if itemSpec.Ref == "" && itemSpec.Contents == nil {
				// No recursive validation needed
				continue
			}

			var subFolderSpec *folderSpec
			if itemSpec.Ref != "" {
				subFolderSpecPath := path.Join(filepath.Dir(s.specPath), itemSpec.Ref)
				subFolderSpec, err = newFolderSpec(s.fs, subFolderSpecPath)
				if err != nil {
					errs = append(errs, err)
					continue
				}
			} else if itemSpec.Contents != nil {
				subFolderSpec = &folderSpec{
					fs:        s.fs,
					itemSpecs: itemSpec.Contents,
					specPath:  s.specPath,
				}
			}

			subFolderPath := path.Join(folderPath, fileName)
			subErrs := subFolderSpec.validate(subFolderPath)
			if len(subErrs) > 0 {
				errs = append(errs, subErrs...)
			}

		} else {
			if !itemSpec.isSameType(file) {
				errs = append(errs, fmt.Errorf("[%s] is a file but is expected to be a folder", fileName))
				continue
			}
			// TODO: more validation for file item
		}
	}

	// validate that required items in spec are all accounted for
	for _, itemSpec := range s.itemSpecs {
		if !itemSpec.Required {
			continue
		}

		fileFound, err := itemSpec.matchingFileExists(files)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if !fileFound {
			var err error
			if itemSpec.Name != "" {
				err = fmt.Errorf("expecting to find %s matching name [%s] in folder [%s]", itemSpec.ItemType, itemSpec.Name, folderPath)
			} else if itemSpec.Pattern != "" {
				err = fmt.Errorf("expecting to find %s matching pattern [%s] in folder [%s]", itemSpec.ItemType, itemSpec.Pattern, folderPath)
			}
			errs = append(errs, err)
		}
	}
	return errs
}

func (s *folderSpec) findItemSpec(folderItemName string) (*folderItemSpec, error) {
	for _, itemSpec := range s.itemSpecs {
		if itemSpec.Name != "" && itemSpec.Name == folderItemName {
			return &itemSpec, nil
		}
		if itemSpec.Pattern != "" {
			isMatch, err := regexp.MatchString(itemSpec.Pattern, folderItemName)
			if err != nil {
				return nil, errors.Wrap(err, "invalid folder item spec pattern")
			}
			if isMatch {
				return &itemSpec, nil
			}
		}
	}

	// No item spec found
	return nil, nil
}

func (s *folderItemSpec) matchingFileExists(files []os.FileInfo) (bool, error) {
	if s.Name != "" {
		for _, file := range files {
			if file.Name() == s.Name {
				return s.isSameType(file), nil
			}
		}
	} else if s.Pattern != "" {
		for _, file := range files {
			isMatch, err := regexp.MatchString(s.Pattern, file.Name())
			if err != nil {
				return false, errors.Wrap(err, "invalid folder item spec pattern")
			}
			if isMatch {
				return s.isSameType(file), nil
			}
		}
	}

	return false, nil
}

func (s *folderItemSpec) isSameType(file os.FileInfo) bool {
	switch s.ItemType {
	case ItemTypeFile:
		return !file.IsDir()
	case ItemTypeFolder:
		return file.IsDir()
	}

	return false
}
