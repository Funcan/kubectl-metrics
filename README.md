kubectl-metrics
===============

A kubectl plugin to display a ppod's prometheus exported metrics in a tabular
format.

It finds exposed metrics either from prometheus CRs or from the pod's
annotations.

Prints the name and type of each metric, and optionally the metric's value.
Useful for detecting if metrics produced by a pod are the same before and
after an upgrade, or for quickly checking if a pod is producing the expected
metrics.
