package main

import (
	"fmt"
	"math"
	"strings"
)

/*
GetMainControllerName Will get the main owner controller name from a given pod name.
*/
func GetMainControllerName(podName string) string {
	podNameTuple := strings.Split(podName, "-")
	controllerNameTuple := podNameTuple[:len(podNameTuple)-1]
	return fmt.Sprintf(strings.Join(controllerNameTuple, "-"))
}

/*
RoundUPAndStringify Rounds up to the nearest 100th figure and outputs as string.
*/
func RoundUPAndStringify(num float64, metric string) string {
	switch metric {
	case "cpu":
		return fmt.Sprintf("%vm", int(math.Ceil(num/100.0))*100)
	case "mem":
		return fmt.Sprintf("%vMi", int(math.Ceil(num/100.0))*100)
	default:
		return fmt.Sprintf("%vm", int(math.Ceil(num/100.0))*100)
	}
}

/*
SplitKeysWithContainerName This will get the pod container name from prometheus
Vector metrics keys.
*/
func SplitKeysWithContainerName(key string) string {
	var containerName string
	splitKeys := strings.Split(key, ",")[0]
	if strings.Contains(splitKeys, "container") {
		splitContainerName := strings.Split(splitKeys, "=")[1]
		containerName = strings.Trim(splitContainerName, "\"")
		return containerName
	}
	return ""
}
