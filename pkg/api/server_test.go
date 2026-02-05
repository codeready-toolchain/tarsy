package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	
	testdb "github.com/codeready-toolchain/tarsy/test/database"
)

// TestDatabaseImport verifies that test/database package can be imported and used
func TestDatabaseImport(t *testing.T) {
	// This demonstrates that test/database package is accessible from other packages
	// Full integration tests will be added in Phase 2.3
	dbClient := testdb.NewTestClient(t)
	
	assert.NotNil(t, dbClient)
	assert.NotNil(t, dbClient.DB())
}
