# OSK Support Manual Testing Results

**Date:** 2026-03-05
**Tester:** Claude Code (Sonnet 4.5)
**Build Version:** 0.0.0-localdev
**Commit:** a812d92c32e0ec692868e76cdcbedd292b5ad2c2

## Test Environment

- Docker and Docker Compose: Installed
- kcp binary built successfully: 91M
- Test environments: docker-compose-plaintext.yml and docker-compose-kraft.yml

## Test Results Summary

All tests **PASSED**. No issues encountered.

---

## Step 1: Build kcp binary

**Status:** PASS

**Command:**
```bash
make build-frontend
make build
```

**Output:**
- Frontend build completed in 9.37s
- Go binary built successfully: `/Users/tom.underhill/dev/kcp/kcp` (91M)
- Build version: 0.0.0-localdev

---

## Step 2: Test OSK plaintext scanning

**Status:** PASS

**Commands:**
```bash
make test-env-up-plaintext
./kcp scan clusters --source-type osk \
  --credentials-file test/credentials/osk-credentials-plaintext.yaml \
  --state-file test-osk-state.json
```

**Key Output:**
```
2026/03/05 13:02:13 INFO successfully scanned OSK cluster cluster=test-kafka-plaintext cluster_id=DIzu1J2yRxeNtlA1AVYAaQ topics=2 acls=0
✅ Scan completed successfully
   Scanned 1 cluster(s)
   State file: /Users/tom.underhill/dev/kcp/test-osk-state.json
```

**Verification:**
```bash
cat test-osk-state.json | jq '.osk_sources.clusters[0].id'
# Output: "test-kafka-plaintext"
```

**Result:** Cluster ID correctly identified as `test-kafka-plaintext`.

---

## Step 3: Test OSK KRaft scanning

**Status:** PASS

**Commands:**
```bash
make test-env-down
make test-env-up-kraft
./kcp scan clusters --source-type osk \
  --credentials-file test/credentials/osk-credentials-kraft.yaml \
  --state-file test-kraft-state.json
```

**Key Output:**
```
2026/03/05 13:02:46 INFO successfully scanned OSK cluster cluster=test-kafka-kraft cluster_id=MkU3OEVBNTcwNTJENDM2Qg topics=2 acls=0
✅ Scan completed successfully
   Scanned 1 cluster(s)
   State file: /Users/tom.underhill/dev/kcp/test-kraft-state.json
```

**Verification:**
```bash
cat test-kraft-state.json | jq '.osk_sources.clusters[0].id'
# Output: "test-kafka-kraft"
```

**Result:** KRaft cluster correctly scanned with ID `test-kafka-kraft`.

---

## Step 4: Test incremental scan (same cluster twice)

**Status:** PASS

**Commands:**
```bash
make test-env-down
make test-env-up-plaintext
./kcp scan clusters --source-type osk \
  --credentials-file test/credentials/osk-credentials-plaintext.yaml \
  --state-file test-osk-state.json  # Re-scan to same file
```

**Key Output:**
```
2026/03/05 13:03:20 INFO loaded existing state file file=/Users/tom.underhill/dev/kcp/test-osk-state.json
2026/03/05 13:03:20 INFO successfully scanned OSK cluster cluster=test-kafka-plaintext cluster_id=rdv-sUwQTSGhU87hmd_DMA topics=2 acls=0
2026/03/05 13:03:20 INFO merged OSK scan results clusters=1
```

**Verification:**
```bash
cat test-osk-state.json | jq '.osk_sources.clusters | length'
# Output: 1
```

**Result:** Incremental scan correctly detected duplicate cluster and did not create a second entry. State file still contains exactly 1 cluster.

---

## Step 5: Test error handling - invalid credentials

**Status:** PASS

**Commands:**
```bash
cat > test/credentials/invalid.yaml <<'EOF'
clusters:
  - id: test
    bootstrap_servers: []  # Invalid - empty
    auth_method:
      sasl_scram:
        use: true
EOF

./kcp scan clusters --source-type osk \
  --credentials-file test/credentials/invalid.yaml \
  --state-file test-error.json
```

**Error Output:**
```
Error: failed to load credentials: failed to load OSK credentials: [cluster[0] (id=test): no bootstrap servers specified cluster[0] (id=test): sasl_scram username is required]
```

**Result:** Clear, actionable error messages for:
1. Missing/empty bootstrap servers
2. Missing SASL/SCRAM username

Validation correctly prevented scan from starting with invalid credentials.

---

## Step 6: Test error handling - wrong source type

**Status:** PASS

**Command:**
```bash
./kcp scan clusters --source-type invalid \
  --credentials-file test/credentials/osk-credentials-plaintext.yaml \
  --state-file test-error2.json
```

**Error Output:**
```
Error: invalid source-type 'invalid': must be 'msk' or 'osk'
```

**Result:** Clear validation error exactly as expected. Source type validation working correctly.

---

## Step 7: Verify scanned data contains topics

**Status:** PASS

**Command:**
```bash
cat test-osk-state.json | jq '.osk_sources.clusters[0].kafka_admin_client_information.topics.details[] | .name'
```

**Output:**
```
"test-topic-2"
"test-topic-1"
```

**Result:** Both test topics from the setup script were successfully discovered and stored in the state file.

---

## Step 8: Clean up

**Status:** PASS

**Command:**
```bash
make test-env-down
rm -f test-*.json test/credentials/invalid.yaml
```

**Result:** All test artifacts cleaned up successfully.

---

## Overall Assessment

**Status:** ALL TESTS PASSED

The OSK implementation is working correctly with:

1. **Plaintext ZooKeeper clusters** - Scans successfully
2. **KRaft mode clusters** - Scans successfully
3. **Incremental scans** - Correctly merges duplicate clusters without creating duplicates
4. **Error handling** - Clear validation messages for:
   - Invalid/empty credentials
   - Wrong source types
5. **Data integrity** - Topics and cluster metadata correctly captured in state file
6. **State file operations** - Proper creation, loading, and merging of state

## Notable Observations

1. **Warning message:** The scan shows `WARN credentials file should be named 'osk-credentials.yaml' for OSK sources` when using test credentials files with different names. This is expected behavior and helps guide users to the correct naming convention.

2. **Cluster ID generation:** Each new Docker container instance generates a different cluster ID (as expected), but the user-defined ID from credentials file (`test-kafka-plaintext`, `test-kafka-kraft`) is correctly used for state management.

3. **Topic discovery:** Both test topics created by the setup script are discovered correctly.

4. **ACL discovery:** Correctly reports 0 ACLs (expected for unauthenticated test clusters).

5. **Connector discovery:** Correctly skips connector scan when `connect-configs` topic doesn't exist.

## Recommendations

Implementation is ready for production use. No issues or bugs found during manual testing.
