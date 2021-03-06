package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"strings"
)

func namespaceSeparator() string {
	return ":"
}

func processBytes(byteArray []byte, output *string) (string, int, int, int, error) {

	//preflight with optional conversion from YAMLs
	err := preflightAsset(&byteArray)
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("input failed preflight check: %v", err)
	}

	// make sure config objects are presented as a list
	err = makeList(&byteArray)
	if err != nil {
		return "", 0, 0, 0, err
	}

	var apiObjectSet APIObjectSet

	if err = json.Unmarshal(byteArray, &apiObjectSet); err != nil {
		return "", 0, 0, 0, fmt.Errorf("can't unmarshal data: %v", err)
	}

	// extract compact datastructures
	namespacePodMap := make(map[string][]string)
	namespaceLabelMap := make(map[string]map[string]string)
	podLabelMap := make(map[string]map[string]string)
	networkPolicyNamespaces := make(map[string]struct{})
	networkPolicies := []APIObject{}
	for _, apiObject := range apiObjectSet.APIObjects {
		namespace := apiObject.Metadata.Namespace
		switch apiObject.Kind {

		case "Pod":
			if onBlacklist(namespace) {
				continue
			}

			if apiObject.Status != nil &&
				len(apiObject.Status.ContainerStatuses) > 0 &&
				apiObject.Status.ContainerStatuses[0].Ready == true {
				slice := []string{namespace, apiObject.Metadata.Name}
				qualifiedPodName := strings.Join(slice, namespaceSeparator())
				namespacePodMap[namespace] = append(namespacePodMap[namespace], qualifiedPodName)
				podLabelMap[qualifiedPodName] = apiObject.Metadata.Labels
			}

		case "NetworkPolicy":
			if onBlacklist(namespace) {
				continue
			}
			networkPolicies = append(networkPolicies, *apiObject)
			networkPolicyNamespaces[namespace] = struct{}{}

		case "Namespace":
			namespaceLabelMap[apiObject.Metadata.Name] = apiObject.Metadata.Labels
		}
	}

	edgeMap := make(map[string][]string)
	initializeEdgeMap(&edgeMap, &namespacePodMap)
	deduplicateEdgeMap(&edgeMap)
	allEdgesCount := countEdges(&edgeMap)

	namespaceEdgeMap := edgeMap
	filterIntraNamespace(&namespaceEdgeMap)
	allNamespaceEdgesCount := countEdges(&namespaceEdgeMap)

	// two passes req'd: isolation, then whitelisting
	filterEdgeMap(&edgeMap, &namespacePodMap, &namespaceLabelMap, &podLabelMap, &networkPolicies, FilterIsolation)
	filterEdgeMap(&edgeMap, &namespacePodMap, &namespaceLabelMap, &podLabelMap, &networkPolicies, FilterWhitelist)
	filteredEdgesCount := countEdges(&edgeMap)

	// metric percentage isolated
	var percentageIsolatedInt int
	percentageIsolatedInt = 100
	if allEdgesCount != 0 {
		var percentageIsolated float64
		percentageIsolated = 100.0 - (float64(filteredEdgesCount)/float64(allEdgesCount))*100.0
		percentageIsolatedInt = int(math.Floor(percentageIsolated + 0.5))
	}

	// metric percentage namespace policy coverage
	var percentageNamespaceCoverageInt int
	percentageNamespaceCoverageInt = 100
	if len(namespacePodMap) != 0 {
		var percentageNamespaceCoverage float64
		percentageNamespaceCoverage = (float64(len(networkPolicyNamespaces)) / float64(len(namespacePodMap))) * 100.0
		percentageNamespaceCoverageInt = int(math.Floor(percentageNamespaceCoverage + 0.5))
	}

	// metric percentage isolated - ignoring intra-namespace connections
	var percentageIsolatedNamespaceInt int
	percentageIsolatedNamespaceInt = 100
	if allEdgesCount != 0 {
		filterIntraNamespace(&edgeMap)
		filteredEdgesCount = countEdges(&edgeMap)
		var percentageIsolatedNamespace float64
		percentageIsolatedNamespace = 100.0 - (float64(filteredEdgesCount)/float64(allNamespaceEdgesCount))*100.0
		percentageIsolatedNamespaceInt = int(math.Floor(percentageIsolatedNamespace + 0.5))
	}

	var buffer bytes.Buffer
	switch *output {
	case "dot":
		writeDot(&namespacePodMap, &edgeMap, &buffer)
	case "json":
		writeJSON(percentageIsolatedInt, percentageIsolatedNamespaceInt, percentageNamespaceCoverageInt, &buffer)
	case "yaml":
		writeYaml(percentageIsolatedInt, percentageIsolatedNamespaceInt, percentageNamespaceCoverageInt, &buffer)
	case "markdown":
		writeMarkdown(percentageIsolatedInt, percentageNamespaceCoverageInt, &buffer)
	}

	return buffer.String(), percentageIsolatedInt, percentageIsolatedNamespaceInt, percentageNamespaceCoverageInt, nil
}

func processFile(path string, output *string) (string, error) {
	byteArray, err := ioutil.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("can't read %s: %v", path, err)
	}

	result, _, _, _, err := processBytes(byteArray, output)

	if err != nil {
		return "", fmt.Errorf("can't process %s: %s", path, err)
	}

	return result, nil
}

func countEdges(edgeMap *map[string][]string) int {
	count := 0
	for _, v := range *edgeMap {
		for range v {
			count++
		}
	}
	return count
}
