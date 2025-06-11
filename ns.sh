#!/bin/bash
read -p "Enter the namespace number: " num
ns_name="test-yellow-ns-$num"

echo "create ns $ns_name"
kubectl create ns $ns_name
echo "label ns $ns_name snappcloud.io/team=unknown"
kubectl label ns $ns_name snappcloud.io/team=unknown --overwrite
echo "annotate ns $ns_name snappcloud.io/requester="sss""
kubectl annotate ns $ns_name snappcloud.io/requester="sss" -n $ns_name
