package helm

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform/helper/acctest"
	"github.com/hashicorp/terraform/helper/resource"
)

// These tests are kept to test backwards compatibility for the helm_repository resource

func TestAccResourceRepository_basic(t *testing.T) {
	name := fmt.Sprintf("%s-%s", testRepositoryName, acctest.RandString(10))
	namespace := fmt.Sprintf("%s-%s", testNamespace, acctest.RandString(10))
	// Note: this helm resource does not automatically create namespaces so no cleanup needed here

	resource.Test(t, resource.TestCase{
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckHelmReleaseDestroy(namespace),
		Steps: []resource.TestStep{{
			Config: testAccHelmRepositoryConfigBasic(name, testRepositoryURL),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("helm_repository.test", "metadata.0.name", name),
				resource.TestCheckResourceAttr("helm_repository.test", "metadata.0.url", testRepositoryURL),
			),
		}, {
			Config: testAccHelmRepositoryConfigBasic(name, testRepositoryURL),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("helm_repository.test", "metadata.0.name", name),
				resource.TestCheckResourceAttr("helm_repository.test", "metadata.0.url", testRepositoryURL),
			),
		}, {
			Config: testAccHelmRepositoryConfigBasic(name, testRepositoryURLAlt),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("helm_repository.test", "metadata.0.name", name),
				resource.TestCheckResourceAttr("helm_repository.test", "metadata.0.url", testRepositoryURLAlt),
			),
		}},
	})
}

func testAccHelmRepositoryConfigBasic(name, url string) string {
	return fmt.Sprintf(`
		resource "helm_repository" "test" {
 			name = %q
			url  = %q
		}
	`, name, url)
}
