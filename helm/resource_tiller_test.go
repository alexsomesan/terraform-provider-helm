package helm

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/terraform"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAccResourceTiller_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		Providers:    testAccProviders,
		PreCheck:     testAccSetupNamespace,
		CheckDestroy: testAccCheckHelmTillerDestroy,
		Steps: []resource.TestStep{{
			Config: testAccHelmTillerConfigBasic(testNamespace),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("helm_tiller.test", "metadata.0.name", deploymentName),
				resource.TestCheckResourceAttr("helm_tiller.test", "metadata.0.namespace", testNamespace),
				resource.TestCheckResourceAttr("helm_tiller.test", "metadata.0.generation", "1"),
			),
		}},
	})
}

func testAccHelmTillerConfigBasic(namespace string) string {
	return fmt.Sprintf(`
		resource "helm_tiller" "test" {
 			namespace = %q
		}
	`, namespace)
}

func testAccCheckHelmTillerDestroy(s *terraform.State) error {
	m := testAccProvider.Meta()
	if m == nil {
		return fmt.Errorf("provider not properly initialized")
	}

	obj, err := m.(*Meta).K8sClient.Extensions().Deployments(testNamespace).Get(deploymentName, metav1.GetOptions{})
	if err == nil {
		// if it still exists, check for deletion timestamp
		if obj.GetObjectMeta().GetDeletionTimestamp() == nil {
			return fmt.Errorf("Tiller deployment should have a deletion timestamp after destroy")
		}
	}

	return nil
}

func testAccSetupNamespace() {
	m := testAccProvider.Meta()
	_, err := m.(*Meta).K8sClient.CoreV1().Namespaces().Get(testNamespace, metav1.GetOptions{})
	ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}}
	if err != nil {
		// create test namespace if it does not exist
		m.(*Meta).K8sClient.CoreV1().Namespaces().Create(ns)
	}
}
