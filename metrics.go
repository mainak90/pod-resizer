package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	slog "github.com/sirupsen/logrus"
	"os"
	"sort"
	"strconv"
	"time"
)

/*
ResourceAllocables Full structure of all is needed to get full info of allocating resource.
*/
type ResourceAllocables struct {
	Name         string             `json:"name"`
	AvgCpu       map[string]float64 `json:"avgCpu"`
	AvgMem       map[string]float64 `json:"avgMem"`
	PeakCPU      map[string]float64 `json:"peakCPU"`
	PeakMem      map[string]float64 `json:"peakMem"`
	BaseCPUQuery string             `json:"baseCPUQuery"`
	BaseMemQuery string             `json:"baseMemQuery"`
	PeakCPUQuery string             `json:"peakCPUQuery"`
	PeakMemQuery string             `json:"peakMemQuery"`
}

/*
QueryBuilder will generate the actual prometheus query by casting the actual pod details into the
placeholder query.
*/
func (resAlloc *ResourceAllocables) QueryBuilder() {
	resAlloc.BaseCPUQuery = fmt.Sprintf("sum(rate(container_cpu_usage_seconds_total{pod=~\"%s-.+\"}[7d])) by (pod, container) * 1000", resAlloc.Name)
	resAlloc.BaseMemQuery = fmt.Sprintf("avg(container_memory_working_set_bytes{pod=~\"%s-.+\"}) by (pod, container) / 1000000", resAlloc.Name)
	resAlloc.PeakCPUQuery = fmt.Sprintf("max_over_time(rate(container_cpu_usage_seconds_total{pod=~\"%s-.+\"}[1m])[168h:1m]) * 1000", resAlloc.Name)
	resAlloc.PeakMemQuery = fmt.Sprintf("max_over_time(container_memory_working_set_bytes{pod=~\"%s-.+\"}[7d]) / 1000000", resAlloc.Name)
}

type PromConfig struct {
	Address string     `json:"address"`
	Client  api.Client `json:"client"`
}

/*
AssignCPUAllocations This one would just generate Vector output for CPU metrics for the pod in question.
Pulls max and avg CPU metrics and returns a resourceAllocation type.
*/
func (resAlloc *ResourceAllocables) AssignCPUAllocations(client api.Client) error {
	slog.SetFormatter(&slog.JSONFormatter{})

	slog.WithFields(
		slog.Fields{
			"Event":  "AssigningVectorAllocations",
			"Source": "MetricsCollector",
		},
	).Info("Assigning CPU Vector Allocations from metrics store.")

	v1api := v1.NewAPI(client)

	var cpuAllocationToAssign = make(map[string]float64)
	var maxCPUAllocationToAssign = make(map[string]float64)
	var cpuAllocations = make(map[string][]float64)
	var maxCPUAllocations = make(map[string][]float64)
	// query for base CPU usage
	res, warnings, err := v1api.Query(context.Background(), resAlloc.BaseCPUQuery, time.Now())
	if err != nil {
		slog.WithFields(slog.Fields{"Event": "MetricsServerQuery",
			"Source": "MetricsCollector"}).Error(fmt.Sprintf("Error encountered while querying remote metrics store %s", err.Error()))
		return err
	}

	if len(warnings) > 0 {
		slog.WithFields(slog.Fields{"Event": "MetricsCollectionWarn", "Source": "MetricsCollector"}).Warn(warnings)
	}

	switch r := res.(type) {
	case model.Vector:
		if r.Len() == 0 {
			slog.WithFields(slog.Fields{"Event": "MetricsCollectionWarn", "Source": "MetricsCollector"}).Warn("No metrics retrieved for app %s", resAlloc.Name)
		}

		for key, value := range r {
			v, err := strconv.ParseFloat(r[key].Value.String(), 64)
			if err != nil {
				slog.WithFields(slog.Fields{"Event": "MetricsCollectionError", "Source": "MetricsCollector"}).Error("Error parsing float from metrics vector %s", err.Error())
				return err
			}
			containerName := SplitKeysWithContainerName(fmt.Sprintf("%v", value))
			if containerName != "" {
				cpuAllocations[containerName] = append(cpuAllocations[containerName], v)
			}
		}
		/* As cpuAllocations are returned as a vector map of map and slice keys
		this will upsert it into a resourceAllocable quantity of Avg/PeakCPU
		*/
		for key, _ := range cpuAllocations {
			metricsSlice := cpuAllocations[key]
			sort.Float64s(metricsSlice)
			cpuAllocations[key] = metricsSlice
			cpuAllocationToAssign[key] = metricsSlice[len(metricsSlice)-1]
		}
		resAlloc.AvgCpu = cpuAllocationToAssign
		fmt.Println(resAlloc.AvgCpu)
	default:
		return errors.New("type not implemented")
	}
	// Peak CPU query
	maxres, warnings, err := v1api.Query(context.Background(), resAlloc.PeakCPUQuery, time.Now())
	if err != nil {
		slog.WithFields(slog.Fields{"Event": "MetricsCollectionError", "Source": "MetricsCollector"}).Error("Error encountered while querying remote metrics store for app %s %s", resAlloc.Name, err.Error())
		return err
	}

	if len(warnings) > 0 {
		slog.WithFields(slog.Fields{"Event": "MetricsCollectionWarn", "Source": "MetricsCollector"}).Warn(warnings)
	}

	switch maxr := maxres.(type) {
	case model.Vector:
		if maxr.Len() == 0 {
			slog.WithFields(slog.Fields{"Event": "MetricsCollectionWarn", "Source": "MetricsCollector"}).Warn(fmt.Sprintf("No max metrics retrieved for app %s", resAlloc.Name))
		}

		for key, value := range maxr {
			v, err := strconv.ParseFloat(maxr[key].Value.String(), 64)
			if err != nil {
				slog.WithFields(slog.Fields{"Event": "MetricsCollectionError", "Source": "MetricsCollector"}).Error(fmt.Sprintf("Error parsing float from metrics vector %s", err.Error()))
				return err
			}
			containerName := SplitKeysWithContainerName(fmt.Sprintf("%v", value))
			if containerName != "" {
				maxCPUAllocations[containerName] = append(maxCPUAllocations[containerName], v)
			}
		}
		/* As cpuAllocations are returned as a vector map of map and slice keys
		this will upsert it into a resourceAllocable quantity of Avg/PeakCPU
		*/
		for key, _ := range maxCPUAllocations {
			metricsSlice := maxCPUAllocations[key]
			sort.Float64s(metricsSlice)
			maxCPUAllocations[key] = metricsSlice
			maxCPUAllocationToAssign[key] = metricsSlice[len(metricsSlice)-1]
		}
		resAlloc.PeakCPU = maxCPUAllocationToAssign
		fmt.Println(resAlloc.PeakCPU)
	default:
		return errors.New("metric type not implemented")
	}
	return nil
}

func (resAlloc *ResourceAllocables) AssignMemoryAllocations(client api.Client) error {
	slog.SetFormatter(&slog.JSONFormatter{})

	slog.WithFields(
		slog.Fields{
			"Event":  "AssigningVectorAllocations",
			"Source": "MetricsCollector",
		},
	).Info("Assigning Memory Vectors Allocations from metrics store.")

	v1api := v1.NewAPI(client)

	var memoryAllocationToAssign = make(map[string]float64)
	var maxMemoryAllocationToAssign = make(map[string]float64)
	var memoryAllocations = make(map[string][]float64)
	var maxMemoryAllocations = make(map[string][]float64)
	// query for base Memory usage
	res, warnings, err := v1api.Query(context.Background(), resAlloc.BaseMemQuery, time.Now())
	if err != nil {
		slog.WithFields(slog.Fields{"Event": "MetricsServerQuery",
			"Source": "MetricsCollector"}).Error(fmt.Sprintf("Error encountered while querying remote metrics store %s", err.Error()))
		return err
	}

	if len(warnings) > 0 {
		slog.WithFields(slog.Fields{"Event": "MetricsCollectionWarn", "Source": "MetricsCollector"}).Warn(warnings)
	}

	switch r := res.(type) {
	case model.Vector:
		if r.Len() == 0 {
			slog.WithFields(slog.Fields{"Event": "MetricsCollectionWarn", "Source": "MetricsCollector"}).Warn("No metrics retrieved for app %s", resAlloc.Name)
		}

		for key, value := range r {
			v, err := strconv.ParseFloat(r[key].Value.String(), 64)
			if err != nil {
				slog.WithFields(slog.Fields{"Event": "MetricsCollectionError", "Source": "MetricsCollector"}).Error("Error parsing float from metrics vector %s", err.Error())
				return err
			}
			containerName := SplitKeysWithContainerName(fmt.Sprintf("%v", value))
			if containerName != "" {
				memoryAllocations[containerName] = append(memoryAllocations[containerName], v)
			}
		}

		for key, _ := range memoryAllocations {
			metricsSlice := memoryAllocations[key]
			sort.Float64s(metricsSlice)
			memoryAllocations[key] = metricsSlice
			memoryAllocationToAssign[key] = metricsSlice[len(metricsSlice)-1]
		}
		resAlloc.AvgMem = memoryAllocationToAssign
		fmt.Println(resAlloc.AvgMem)
	default:
		return errors.New("type not implemented")
	}
	// query for peak memory usage
	maxres, warnings, err := v1api.Query(context.Background(), resAlloc.PeakMemQuery, time.Now())
	if err != nil {
		slog.WithFields(slog.Fields{"Event": "MetricsCollectionError", "Source": "MetricsCollector"}).Error("Error encountered while querying remote metrics store for app %s %s", resAlloc.Name, err.Error())
		return err
	}

	if len(warnings) > 0 {
		slog.WithFields(slog.Fields{"Event": "MetricsCollectionWarn", "Source": "MetricsCollector"}).Warn(warnings)
	}

	switch maxr := maxres.(type) {
	case model.Vector:
		if maxr.Len() == 0 {
			slog.WithFields(slog.Fields{"Event": "MetricsCollectionWarn", "Source": "MetricsCollector"}).Warn(fmt.Sprintf("No max metrics retrieved for app %s", resAlloc.Name))
		}
		for key, value := range maxr {
			v, err := strconv.ParseFloat(maxr[key].Value.String(), 64)
			if err != nil {
				slog.WithFields(slog.Fields{"Event": "MetricsCollectionError", "Source": "MetricsCollector"}).Error(fmt.Sprintf("Error parsing float from metrics vector %s", err.Error()))
				return err
			}
			containerName := SplitKeysWithContainerName(fmt.Sprintf("%v", value))
			if containerName != "" {
				maxMemoryAllocations[containerName] = append(maxMemoryAllocations[containerName], v)
			}
		}
		/* As memoryAllocations are returned as a vector map of map and slice keys
		this will upsert it into a resourceAllocable quantity of Avg/Peak Memory.
		*/
		for key, _ := range maxMemoryAllocations {
			metricsSlice := maxMemoryAllocations[key]
			sort.Float64s(metricsSlice)
			maxMemoryAllocations[key] = metricsSlice
			maxMemoryAllocationToAssign[key] = metricsSlice[len(metricsSlice)-1]
		}
		resAlloc.PeakMem = maxMemoryAllocationToAssign
		fmt.Println(resAlloc.PeakMem)
	default:
		return errors.New("metric type not implemented")
	}
	return nil
}

/*
Propagate generates the full spec of the ResourceAllocables type and returns to caller.
Once returned the type struct should have everything to set the full requests body of an admissible pod.
*/
func (pc *PromConfig) Propagate(app string) ResourceAllocables {

	pc.Address = os.Getenv("METRICS_ENDPOINT_ADDR")

	slog.WithFields(slog.Fields{"Event": "MetricsCollectorEnvironment", "Source": "MetricsCollector"}).Info(fmt.Sprintf("Metrics endpoint env %s", pc.Address))

	c, err := api.NewClient(api.Config{
		Address: pc.Address,
	})
	if err != nil {
		slog.WithFields(slog.Fields{"Event": "MetricsPropagateError", "Source": "MetricsCollector"}).Error(fmt.Sprintf("Error encountered while building remote client %s", err.Error()))
	}

	pc.Client = c
	var resAlloc = ResourceAllocables{Name: app}
	// Will use the resourceAllocable type predefined query and pass in the variables with placeholders to generate the actual query to make.
	resAlloc.QueryBuilder()

	slog.WithFields(slog.Fields{"Event": "CPUMetricsGeneration", "Source": "MetricsCollector"}).Info(fmt.Sprintf("Starting generation of CPU metrics for app %s", app))

	err = resAlloc.AssignCPUAllocations(pc.Client)
	if err != nil {
		slog.WithFields(slog.Fields{"Event": "CPUMetricsPropagateError", "Source": "MetricsCollector"}).Error(fmt.Sprintf("Setting nil value for CPU allocation peak/avg for app %s as values could not be found", resAlloc.Name))
		resAlloc.PeakCPU = nil
		resAlloc.AvgCpu = nil
		slog.WithFields(slog.Fields{"Event": "CPUMetricsPropagateError", "Source": "MetricsCollector"}).Error(fmt.Sprintf("Error encountered while assigning cpu vector allocation for app %s %s", resAlloc.Name, err.Error()))
	}

	slog.WithFields(slog.Fields{"Event": "MemoryMetricsGeneration", "Source": "MetricsCollector"}).Info(fmt.Sprintf("Starting generation of memory metrics for app %s", app))
	err = resAlloc.AssignMemoryAllocations(pc.Client)
	if err != nil {
		slog.WithFields(slog.Fields{"Event": "CPUMetricsPropagateError", "Source": "MetricsCollector"}).Error(fmt.Sprintf("Setting nil value for memory allocation peak/avg for app %s as values could not be found", resAlloc.Name))
		resAlloc.PeakMem = nil
		resAlloc.AvgMem = nil
		slog.WithFields(slog.Fields{"Event": "CPUMetricsPropagateError", "Source": "MetricsCollector"}).Error(fmt.Sprintf("Error encountered while assigning cpu vector allocation for app %s %s", resAlloc.Name, err.Error()))
	}

	return resAlloc
}
