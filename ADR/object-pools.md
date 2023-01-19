
# Title
object pools for forked repositories

## Status

[x] proposed
[ ] accepted
[ ] rejected
[ ] implemented
[ ] deprecated
[ ] superseded


## Context

Currently, whenever a repository is forked, A copy of parent repository is not created. Instead, the forked repository is linked to the parent reposiory via git alternates. 
Thus, Forked repositories are at the risk of getting corrupted when the objects in the parent repository are deleted.

## Decision

Create object pools, inspired by [gitaly](https://gitlab.com/gitlab-org/gitaly/-/blob/master/doc/object_pools.md).

Now, when a repository is forked, a master copy of the repository is created and both parent/ primary and the forked repositories are linked to one master repository which is guarded against object deletions. This master repository is a object pool of all objects of both primary and forked repositories. The primary repository triggers object fetch into master repository whenever changes are made.

## Outcome

Cons: All existing repositories must be migrated to the object pool implementation


