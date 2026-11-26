# Application probe

Run the helper with explicit Admin and SDK origins, a numeric project ID, a source environment, and the flag name. Use the CLI's normal login or host-scoped credentials. Keep CLI output filtered; never print tokens, full environment documents, or raw sessions into the agent transcript.

The probe is a trusted executable invoked without a shell. It gets the clone's client-side SDK key in `REHEARSAL_SDK_KEY`, its SDK origin in `REHEARSAL_SDK_URL`, and `REHEARSAL_FLAG`. The helper passes only those three variables plus PATH, LANG and SYSTEMROOT if set. The probe must bind its app to this clone and isolate its own resources. It owns cleanup of any application fixtures it creates. Use an isolated OS account or container for untrusted code; filtering environment variables is not a process sandbox. The probe exercises the retirement policies in its test environment.

| Invocation | JSON on stdin | Required JSON on stdout |
| --- | --- | --- |
| `probe capture` | `identity`, `traits`, null `plan` and `snapshot` | One non-null JSON snapshot of in-flight work under the before assignment |
| `probe replay` | The selected `plan`, the original `snapshot`, the same identity and traits | Exactly the declared check names, each with a Boolean or null |

Return true only when the application assertion was observed; false for an observed violation; null for missing evidence. Exit nonzero on infrastructure errors. Repeating a replay must start from the same captured input with clean application state. Before returning a capture, verify an equivalent item can finish on the original path. A snapshot may reference a fixture that the probe restores per plan. Use compact, synthetic JSON. Avoid live customer data. For asynchronous jobs use a bounded completion condition and check observable side effects after retries.

## Try the queue example

The example fetches the clone's assignment from the Flagsmith SDK endpoint and uses a fresh in-memory SQLite database per plan. It exercises the anonymous audience of a Boolean flag. It compares retiring a versioned queue reader immediately with allowing previously encoded jobs to complete. Use your own flag in an authorized test project.

From the skill directory, set `ADMIN_ORIGIN`, `SDK_ORIGIN`, `PROJECT_ID`, `SOURCE_ENV` and `FLAG` to your authorized target.

```sh
python3 scripts/rehearse.py \
  --api-url "$ADMIN_ORIGIN" --sdk-api-url "$SDK_ORIGIN" \
  --project "$PROJECT_ID" --source "$SOURCE_ENV" --flag "$FLAG" \
  --probe examples/queue_probe.py --plan immediate --plan drain \
  --check fresh_legacy_job --check inflight_delivered_once
```

The two plans share an immutable capture. The example expects the old reader to handle fresh work under both policies. The new-format job fails to deliver under immediate retirement and delivers seven units exactly once under the drain policy. Replace both the probe and its expectations for a different application.

Exit 0 means every required observation passed in every plan. Exit 1 means the comparison completed and at least one check failed. Exit 2 means incomplete evidence, an unexpected SDK assignment, a failed command, or unverified cleanup. An inconclusive run can still contain observations from completed plans. The queue example captures work with the flag on. For an off-to-on rehearsal, use `--before off` with a probe that captures work under the off assignment. For targeted users supply `--identity` and repeated `--trait name=value`; implement that audience in the probe too. Targeted rehearsals stop before cloning if the active identity store cannot list overrides reliably or the flag has any identity overrides. Supply the full synthetic traits required by your probe; percentage-based assignments may vary by environment. The SDK convergence wait defaults to 90 seconds per switch; set `--convergence-timeout SECONDS` to match your cache lifetime. A flag hidden from the SDK when disabled cannot prove an explicit false assignment with this helper.
