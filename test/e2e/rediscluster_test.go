// +build e2e_redis_op

package e2e

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	// "k8s.io/apimachinery/pkg/version"

	"sigs.k8s.io/controller-runtime/pkg/client"

	dbv1 "github.com/PayU/redis-operator/api/v1"
	"github.com/PayU/redis-operator/test/framework"
)

func printAZMap(azMap map[string]map[string][]*corev1.Pod) {
	fmt.Println("\n*** Redis cluster AZ map")
	for az, groups := range azMap {
		fmt.Printf("%s\n", az)
		for leaderID, pods := range groups {
			fmt.Printf("Group [%s]:", leaderID)
			for _, pod := range pods {
				fmt.Printf(" %s", pod.Name)
			}
			fmt.Println()
		}
		fmt.Println()
	}
}

func testCreateDeleteRedisCluster(t *testing.T) {

	fmt.Printf("Running test %s\n", t.Name())

	ctx := framework.NewTestCtx(t, t.Name())
	defer ctx.Cleanup()

	// create namespace, roles, rolebindings and CRD
	err := tfw.InitializeDefaultResources(&ctx, defaultConfig.KustomizePath,
		defaultConfig.OperatorImage,
		defaultConfig.Namespace,
		defaultConfig.RedisClusterSetupTimeout)
	Check(err, t.Fatalf, "Failed to initialize resources")

	fmt.Println("[E2E] Finished creating default resources")

	redisCluster, err := tfw.MakeRedisCluster(defaultConfig.RedisClusterYAMLPath)
	Check(err, t.Fatalf, "Failed to make Redis cluster resource")

	err = tfw.CreateRedisClusterAndWaitUntilReady(&ctx, redisCluster, defaultConfig.RedisClusterSetupTimeout)
	Check(err, t.Fatalf, "Failed to create Redis cluster")

	fmt.Println("[E2E] Finished creating redis cluster")

	ctx.PopFinalizer() // removing the Redis finalizer from the context since it is deleted bellow
	err = tfw.DeleteRedisCluster(redisCluster, defaultConfig.RedisClusterSetupTimeout)
	Check(err, t.Fatalf, "Failed to delete Redis cluster")

	fmt.Println("[E2E] Finished deleting redis cluster")
}

func createDefaultCluster(ctx *framework.TestCtx, t *testing.T) (*dbv1.RedisCluster, error) {
	err := tfw.InitializeDefaultResources(ctx, defaultConfig.KustomizePath,
		defaultConfig.OperatorImage, defaultConfig.Namespace, defaultConfig.RedisClusterSetupTimeout)
	Check(err, t.Fatalf, "Failed to initialize resources")

	redisCluster, err := tfw.MakeRedisCluster(defaultConfig.RedisClusterYAMLPath)
	Check(err, t.Fatalf, "Failed to make Redis cluster resource")

	fmt.Println("[E2E] Creating redis cluster...")

	err = tfw.CreateRedisClusterAndWaitUntilReady(ctx, redisCluster, defaultConfig.RedisClusterSetupTimeout)
	Check(err, t.Fatalf, "Failed to create Redis cluster")

	fmt.Println("[E2E] Finished creating redis cluster")

	return redisCluster, err
}

func makeAZMap(ctx *framework.TestCtx, t *testing.T, config TestConfig) map[string]map[string]([]*corev1.Pod) {
	// the topology labels differ between K8s 1.17+ and older
	// https://kubernetes.io/docs/reference/kubernetes-api/labels-annotations-taints/#failure-domainbetakubernetesiozone
	zoneLabel := "failure-domain.beta.kubernetes.io/zone"

	nodes, err := tfw.GetNodes()
	Check(err, t.Fatalf, "Failed to get Kubernetes nodes")

	// AZ map with structure Map[AZ][leaderId] -> pod list
	azMap := make(map[string]map[string]([]*corev1.Pod))

	for _, node := range nodes.Items {
		opts := []client.ListOption{
			client.InNamespace(config.Namespace),
			client.MatchingFields{"spec.nodeName": node.Name},
		}

		leaders, err := tfw.GetRedisPods("leader", opts...)
		Check(err, t.Fatalf, "Failed to get leader nodes")

		followers, err := tfw.GetRedisPods("follower", opts...)
		Check(err, t.Fatalf, "Failed to get follower nodes")

		if len(leaders.Items) == 0 && len(followers.Items) == 0 {
			continue
		}

		if len(leaders.Items)+len(followers.Items) > 1 {
			t.Error("Pod distribution error - found more then one Redis pod on the same node")
		}

		az, foundAZ := azMap[node.Labels[zoneLabel]]
		if !foundAZ {
			az = make(map[string]([]*corev1.Pod))
		}

		for _, pod := range leaders.Items {
			group, found := az[pod.Labels["leader-number"]]
			if !found {
				group = []*corev1.Pod{&pod}
			} else {
				group = append(group, &pod)
			}
			az[pod.Labels["leader-number"]] = group
		}

		for _, pod := range followers.Items {
			group, found := az[pod.Labels["leader-number"]]
			if !found {
				group = []*corev1.Pod{&pod}
			} else {
				group = append(group, &pod)
			}
			az[pod.Labels["leader-number"]] = group
		}

		azMap[node.Labels[zoneLabel]] = az
	}
	return azMap
}

func checkAZCorrectness(azMap map[string]map[string]([]*corev1.Pod), t *testing.T) {
	// check if any AZ has two leaders or two nodes from the same Redis group
	for az, groups := range azMap {
		leaderCount := 0
		for leaderID, pods := range groups {
			for _, pod := range pods {
				if pod.Labels["redis-node-role"] == "leader" {
					leaderCount++
				}
			}
			if len(pods) > 1 {
				t.Errorf("The group with leader ID %s has %d nodes in %s\n", leaderID, len(pods), az)
			}
		}
		if leaderCount > 1 {
			t.Errorf("Found more then one leader in %s\n", az)
		}
	}
}

func testRedisClusterAvailabilityZoneDistribution(t *testing.T) {
	fmt.Printf("---\n[E2E] Running test: %s\n", t.Name())
	ctx := framework.NewTestCtx(t, t.Name())
	defer ctx.Cleanup()

	createDefaultCluster(&ctx, t)
	azMap := makeAZMap(&ctx, t, defaultConfig)
	printAZMap(azMap)
	checkAZCorrectness(azMap, t)
}

func testBasic(t *testing.T) {
	ctx := framework.NewTestCtx(t, t.Name())
	defer ctx.Cleanup()

	_, err := createDefaultCluster(&ctx, t)
	Check(err, t.Fatalf, "Failed to create default cluster")
	err = tfw.PopulateDatabase(defaultConfig.KeyCount, t.Name(), defaultConfig.KeySize)
	Check(err, t.Fatalf, "Failed to populate Redis database")
	fmt.Println("Waiting....")
	time.Sleep(160 * time.Second)
}

/* Rolling update test
1. Create a default Redis cluster
2. Change the Redis container resource requirements and wait for cluster update
*/
func TestRollingUpdate(t *testing.T) {
	fmt.Printf("---\n[E2E] Running test: %s\n", t.Name())
	ctx := framework.NewTestCtx(t, t.Name())
	defer ctx.Cleanup()

	redisCluster, err := createDefaultCluster(&ctx, t)
	Check(err, t.Fatalf, "Failed to create default cluster")
	err = tfw.PopulateDatabase(defaultConfig.KeyCount, t.Name(), defaultConfig.KeySize)
	Check(err, t.Fatalf, "Failed to populate Redis database")

	Check(tfw.ApplyYAMLfile(defaultConfig.RedisClusterUpdatedYAMLPath, defaultConfig.K8sResourceSetupTimeout, false), t.Fatalf, "Failed to update resources in CR")
	Check(tfw.WaitForState(redisCluster, "Updating", defaultConfig.RedisClusterSetupTimeout), t.Fatalf,
		"Failed to reach Updating state")
	fmt.Printf("\n[E2E] Cluster container resource update started...")
	Check(tfw.WaitForState(redisCluster, "Ready", 4*defaultConfig.RedisClusterSetupTimeout), t.Fatalf,
		"Failed to reach Ready state")
	fmt.Printf("\n[E2E] Cluster update successful\n")

	azMap := makeAZMap(&ctx, t, defaultConfig)
	printAZMap(azMap)
	checkAZCorrectness(azMap, t)
}

/* Rolling update test
1. Create a default Redis cluster
2. Set a non-existing Redis image and wait for the controller to detect the failed update
3. Set an existing updated Redis image and wait for cluster update
*/
func testRollingUpdateWithWrongImage(t *testing.T) {
	fmt.Printf("---\n[E2E] Running test: %s\n", t.Name())
	ctx := framework.NewTestCtx(t, t.Name())
	defer ctx.Cleanup()

	redisCluster, err := createDefaultCluster(&ctx, t)
	Check(err, t.Fatalf, "Failed to create default cluster")
	err = tfw.PopulateDatabase(defaultConfig.KeyCount, t.Name(), defaultConfig.KeySize)
	Check(err, t.Fatalf, "Failed to populate Redis database")

	fmt.Printf("\n[E2E] Testing response to missing image...")
	Check(tfw.UpdateRedisImage(redisCluster, "redis:noimage"), t.Fatalf, "Failed to update image in CR")
	Check(tfw.WaitForState(redisCluster, "Updating", defaultConfig.RedisClusterSetupTimeout), t.Fatalf,
		"Failed to reach Updating state")
	fmt.Printf("\n[E2E] Cluster image update started...")
	timeoutErr := tfw.WaitForState(redisCluster, "Ready", 30*time.Second)
	if timeoutErr == nil {
		t.Fatalf("Cluster entered Ready state when update failed")
	}

	fmt.Printf("\n[E2E] Cluster update failed. Restoring...")
	Check(tfw.UpdateRedisImage(redisCluster, defaultConfig.UpdateImage), t.Fatalf, "Failed to update image in CR")
	Check(tfw.WaitForState(redisCluster, "Updating", defaultConfig.RedisClusterSetupTimeout), t.Fatalf,
		"Failed to reach Updating state")
	fmt.Printf("\n[E2E] Cluster image update continued...")
	Check(tfw.WaitForState(redisCluster, "Ready", 4*defaultConfig.RedisClusterSetupTimeout), t.Fatalf,
		"Failed to reach Ready state")
	fmt.Printf("\n[E2E] Cluster update successful\n")

	azMap := makeAZMap(&ctx, t, defaultConfig)
	printAZMap(azMap)
	checkAZCorrectness(azMap, t)
}

/*
1. Create default Redis cluster
2. Drain all nodes of an availability zone
3. Reenable the node scheduling and wait for Redis cluster to recover
*/
func testAZFailure(t *testing.T) {
	fmt.Printf("---\n[E2E] Running test: %s\n", t.Name())
	ctx := framework.NewTestCtx(t, t.Name())
	defer ctx.Cleanup()

	redisCluster, err := createDefaultCluster(&ctx, t)
	Check(err, t.Fatalf, "Failed to create default cluster")
	err = tfw.PopulateDatabase(defaultConfig.KeyCount, t.Name(), defaultConfig.KeySize)
	Check(err, t.Fatalf, "Failed to populate Redis database")

	azMap := makeAZMap(&ctx, t, defaultConfig)
	printAZMap(azMap)
	checkAZCorrectness(azMap, t)

	targetAZ := "eu-central-1a"

	aznodes, err := tfw.GetAvailabilityZoneNodes(targetAZ)
	Check(err, t.Fatalf, "Failed to get AZ nodes from "+targetAZ)

	fmt.Printf("\n[E2E] Disabling availability zone: %s...\n\n", targetAZ)
	for _, node := range aznodes.Items {
		err = tfw.CordonNode(node.Name, true, defaultConfig.K8sResourceSetupTimeout)
		if err != nil {
			tfw.UncordonAll(defaultConfig.K8sResourceSetupTimeout)
			t.Fatalf("Failed to cordon node: %v", err)
		}
		err = tfw.DrainNode(node.Name, defaultConfig.K8sResourceSetupTimeout)
		if err != nil {
			tfw.UncordonAll(defaultConfig.K8sResourceSetupTimeout)
			t.Fatalf("Failed to drain node %s: %v", node.Name, err)
		}
	}

	fmt.Printf("\n[E2E] Enabling availability zone: %s...", targetAZ)
	err = tfw.UncordonAll(defaultConfig.K8sResourceSetupTimeout)
	Check(err, t.Fatalf, "Failed to uncordon nodes")

	Check(tfw.WaitForState(redisCluster, "Recovering", defaultConfig.RedisClusterSetupTimeout), t.Fatalf,
		"Failed to reach Ready state")
	fmt.Printf("\n[E2E] Redis cluster recovering...")

	Check(tfw.WaitForState(redisCluster, "Ready", defaultConfig.RedisClusterSetupTimeout), t.Fatalf,
		"Failed to reach Ready state")

	azMap = makeAZMap(&ctx, t, defaultConfig)
	printAZMap(azMap)
	checkAZCorrectness(azMap, t)
}
