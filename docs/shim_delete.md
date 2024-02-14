Deleting a Shim


- if a shim is deleted, an "uninstall"-job is scheduled on every node that matched the shim's selector


What happens if an uninstall job does not complete (successfully)?

- the deletion of the shim must not be blocked by uninstall-jobs
- node should still be annotated with "uninstall" or "failed"
