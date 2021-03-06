// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"fmt"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t projects.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -e project names
//go:generate mapper stmt -e project names-by-Name
//go:generate mapper stmt -e project objects
//go:generate mapper stmt -e project objects-by-Name
//go:generate mapper stmt -e project used-by-ref
//go:generate mapper stmt -e project used-by-ref-by-Name
//go:generate mapper stmt -e project config-ref
//go:generate mapper stmt -e project config-ref-by-Name
//go:generate mapper stmt -e project create
//go:generate mapper stmt -e project create-config-ref
//go:generate mapper stmt -e project id
//go:generate mapper stmt -e project rename
//go:generate mapper stmt -e project update
//go:generate mapper stmt -e project delete
//
//go:generate mapper method -e project URIs
//go:generate mapper method -e project List
//go:generate mapper method -e project Get
//go:generate mapper method -e project ConfigRef
//go:generate mapper method -e project Exists
//go:generate mapper method -e project Create
//go:generate mapper method -e project UsedByRef
//go:generate mapper method -e project ID
//go:generate mapper method -e project Rename
//go:generate mapper method -e project Delete

// ProjectFilter can be used to filter results yielded by ProjectList.
type ProjectFilter struct {
	Name string // If non-empty, return only the project with this name.
}

// ProjectHasProfiles is a helper to check if a project has the profiles
// feature enabled.
func (c *ClusterTx) ProjectHasProfiles(name string) (bool, error) {
	return projectHasProfiles(c.tx, name)
}

// GetProjectNames returns the names of all available projects.
func (c *ClusterTx) GetProjectNames() ([]string, error) {
	stmt := "SELECT name FROM projects"

	names, err := query.SelectStrings(c.tx, stmt)
	if err != nil {
		return nil, errors.Wrap(err, "Fetch project names")
	}

	return names, nil
}

// GetProjectIDsToNames returns a map associating each project ID to its
// project name.
func (c *ClusterTx) GetProjectIDsToNames() (map[int64]string, error) {
	stmt := "SELECT id, name FROM projects"

	rows, err := c.tx.Query(stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[int64]string{}
	for i := 0; rows.Next(); i++ {
		var id int64
		var name string

		err := rows.Scan(&id, &name)
		if err != nil {
			return nil, err
		}

		result[id] = name
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return result, nil
}

func projectHasProfiles(tx *sql.Tx, name string) (bool, error) {
	stmt := `
SELECT projects_config.value
  FROM projects_config
  JOIN projects ON projects.id=projects_config.project_id
 WHERE projects.name=? AND projects_config.key='features.profiles'
`
	values, err := query.SelectStrings(tx, stmt, name)
	if err != nil {
		return false, errors.Wrap(err, "Fetch project config")
	}

	if len(values) == 0 {
		return false, nil
	}

	return shared.IsTrue(values[0]), nil
}

// ProjectHasImages is a helper to check if a project has the images
// feature enabled.
func (c *ClusterTx) ProjectHasImages(name string) (bool, error) {
	project, err := c.GetProject(name)
	if err != nil {
		return false, errors.Wrap(err, "fetch project")
	}

	enabled := shared.IsTrue(project.Config["features.images"])

	return enabled, nil
}

// UpdateProject updates the project matching the given key parameters.
func (c *ClusterTx) UpdateProject(name string, object api.ProjectPut) error {
	id, err := c.GetProjectID(name)
	if err != nil {
		return errors.Wrap(err, "Fetch project ID")
	}

	stmt := c.stmt(projectUpdate)
	result, err := stmt.Exec(object.Description, id)
	if err != nil {
		return errors.Wrap(err, "Update project")
	}

	n, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "Fetch affected rows")
	}
	if n != 1 {
		return fmt.Errorf("Query updated %d rows instead of 1", n)
	}

	// Clear config.
	_, err = c.tx.Exec(`
DELETE FROM projects_config WHERE projects_config.project_id = ?
`, id)
	if err != nil {
		return errors.Wrap(err, "Delete project config")
	}

	// Insert new config.
	stmt = c.stmt(projectCreateConfigRef)
	for key, value := range object.Config {
		if value == "" {
			continue
		}

		_, err := stmt.Exec(id, key, value)
		if err != nil {
			return errors.Wrap(err, "Insert config for project")
		}
	}

	return nil
}

// InitProjectWithoutImages updates populates the images_profiles table with
// all images from the default project when a project is created with
// features.images=false.
func (c *ClusterTx) InitProjectWithoutImages(project string) error {
	defaultProfileID, err := c.GetProfileID(project, "default")
	if err != nil {
		return errors.Wrap(err, "Fetch project ID")
	}
	stmt := `INSERT INTO images_profiles (image_id, profile_id)
	SELECT images.id, ? FROM images WHERE project_id=1`
	_, err = c.tx.Exec(stmt, defaultProfileID)
	return err
}
