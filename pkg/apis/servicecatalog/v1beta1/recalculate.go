package v1beta1

import "fmt"

func (in *ServiceBroker) RecalculatePrinterColumnStatusFields() {
	in.Status.LastConditionState = serviceBrokerLastConditionState(&in.Status.CommonServiceBrokerStatus)
}

func (in *ClusterServiceBroker) RecalculatePrinterColumnStatusFields() {
	in.Status.LastConditionState = serviceBrokerLastConditionState(&in.Status.CommonServiceBrokerStatus)
}

func (in *ServiceInstance) RecalculatePrinterColumnStatusFields() {
	var class, plan string
	if in.Spec.ClusterServiceClassSpecified() && in.Spec.ClusterServicePlanSpecified() {
		class = fmt.Sprintf("ClusterServiceClass/%s", in.Spec.GetSpecifiedClusterServiceClass())
		plan = in.Spec.GetSpecifiedClusterServicePlan()
	} else {
		class = fmt.Sprintf("ServiceClass/%s", in.Spec.GetSpecifiedServiceClass())
		plan = in.Spec.GetSpecifiedServicePlan()
	}
	in.Status.UserSpecifiedClassName = class
	in.Status.UserSpecifiedPlanName = plan

	in.Status.LastConditionState = getServiceInstanceLastConditionState(&in.Status)
}

func (in *ServiceBinding) RecalculatePrinterColumnStatusFields() {
	in.Status.LastConditionState = getServiceBindingLastConditionState(in.Status)
}

func (in *ServiceInstance) IsUserSpecifiedClassOrPlanExists() bool {
	return in.Status.UserSpecifiedPlanName != "" ||
		in.Status.UserSpecifiedClassName != ""
}


func getServiceInstanceLastConditionState(status *ServiceInstanceStatus) string {
	if len(status.Conditions) > 0 {
		condition := status.Conditions[len(status.Conditions)-1]
		if condition.Status == ConditionTrue {
			return string(condition.Type)
		}
		return condition.Reason
	}
	return ""
}

func serviceBrokerLastConditionState(status *CommonServiceBrokerStatus) string {
	if len(status.Conditions) > 0 {
		condition := status.Conditions[len(status.Conditions)-1]
		if condition.Status == ConditionTrue {
			return string(condition.Type)
		}
		return condition.Reason
	}
	return ""
}

func getServiceBindingLastConditionState(status ServiceBindingStatus) string {
	if len(status.Conditions) > 0 {
		condition := status.Conditions[len(status.Conditions)-1]
		if condition.Status == ConditionTrue {
			return string(condition.Type)
		}
		return condition.Reason
	}
	return ""
}
