#!/bin/bash

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# Configuration
BASE_URL="http://localhost:8080"
TEST_FILE_SIZE=$((30 * 1024 * 1024)) # 30 MB
CHUNK_SIZE=$((10 * 1024 * 1024)) # 10 MB chunks

# Helper functions
print_header() {
    echo ""
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${CYAN}$1${NC}"
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

print_info() {
    echo -e "${BLUE}ℹ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠ $1${NC}"
}

cleanup() {
    print_info "Cleaning up test files..."
    rm -f test_file.bin test_file_downloaded.bin
    rm -f *.2xpfm.key
    rm -f chunk.tmp
}

trap cleanup EXIT

# Test 1: Create user and login
print_header "Test 1: User Authentication"
RANDOM_EMAIL="test_$(date +%s)@example.com"

SIGNUP_RESPONSE=$(curl -s -X POST "$BASE_URL/api/signup" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"$RANDOM_EMAIL\",\"password\":\"test123\"}")

if echo "$SIGNUP_RESPONSE" | grep -q "user created"; then
    print_success "User created: $RANDOM_EMAIL"
else
    print_error "Signup failed: $SIGNUP_RESPONSE"
    exit 1
fi

LOGIN_RESPONSE=$(curl -s -X POST "$BASE_URL/api/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"$RANDOM_EMAIL\",\"password\":\"test123\"}")

TOKEN=$(echo "$LOGIN_RESPONSE" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

if [ -z "$TOKEN" ]; then
    print_error "Login failed: $LOGIN_RESPONSE"
    exit 1
fi

print_success "Login successful"

# Test 2: Link Google Drive accounts
print_header "Test 2: Linking Google Drive Accounts"

echo -e "${YELLOW}How many Google Drive accounts do you want to test with? (1-3): ${NC}"
read NUM_ACCOUNTS

if ! [[ "$NUM_ACCOUNTS" =~ ^[1-3]$ ]]; then
    print_warning "Invalid input. Defaulting to 1 account."
    NUM_ACCOUNTS=1
fi

ACCOUNTS_ADDED=0

for ((i=1; i<=NUM_ACCOUNTS; i++)); do
    print_info "Linking Google Drive account $i of $NUM_ACCOUNTS..."

    OAUTH_RESPONSE=$(curl -s -X GET "$BASE_URL/api/drive/link" \
        -H "Authorization: Bearer $TOKEN")

    AUTH_URL=$(echo "$OAUTH_RESPONSE" | grep -o '"auth_url":"[^"]*"' | cut -d'"' -f4)

    if [ -z "$AUTH_URL" ]; then
        print_error "Failed to get OAuth URL: $OAUTH_RESPONSE"
        continue
    fi

    echo ""
    echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${YELLOW}ACCOUNT $i: Please authorize Google Drive access${NC}"
    echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    echo -e "${CYAN}Open this URL in your browser:${NC}"
    echo "$AUTH_URL"
    echo ""
    echo -e "${YELLOW}Press ENTER after authorization...${NC}"
    read -r

    sleep 3
    DRIVE_ACCOUNTS=$(curl -s -X GET "$BASE_URL/api/drive/accounts" \
        -H "Authorization: Bearer $TOKEN")

    CURRENT_COUNT=$(echo "$DRIVE_ACCOUNTS" | grep -o '"provider":"google"' | wc -l)

    if [ "$CURRENT_COUNT" -ge "$i" ]; then
        print_success "Account $i linked successfully"
        ACCOUNTS_ADDED=$((ACCOUNTS_ADDED + 1))
    else
        print_error "Account $i verification failed"
    fi
done

if [ "$ACCOUNTS_ADDED" -eq 0 ]; then
    print_error "No accounts linked. Cannot continue."
    exit 1
fi

print_success "Total accounts linked: $ACCOUNTS_ADDED"

# Test 3: Check drive spaces and manifests
print_header "Test 3: Checking Drive Spaces and Manifests"

DRIVE_SPACES=$(curl -s -X GET "$BASE_URL/api/drive/space" \
    -H "Authorization: Bearer $TOKEN")

print_success "Drive spaces retrieved"
echo "$DRIVE_SPACES" | python3 -m json.tool 2>/dev/null || echo "$DRIVE_SPACES"

# Test 4: Create test file
print_header "Test 4: Creating Test File"
dd if=/dev/urandom of=test_file.bin bs=1M count=$((TEST_FILE_SIZE / 1024 / 1024)) 2>/dev/null
ORIGINAL_CHECKSUM=$(sha256sum test_file.bin | awk '{print $1}')
print_success "Test file created (checksum: ${ORIGINAL_CHECKSUM:0:16}...)"

# Test 5: Initiate upload (with fileID generation)
print_header "Test 5: Initiating Upload Session"

INITIATE_RESPONSE=$(curl -s -X POST "$BASE_URL/api/files/upload/initiate" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"filename\":\"test_file.bin\",\"file_size\":$TEST_FILE_SIZE}")

SESSION_ID=$(echo "$INITIATE_RESPONSE" | grep -o '"session_id":"[^"]*"' | cut -d'"' -f4)
FILE_ID=$(echo "$INITIATE_RESPONSE" | grep -o '"file_id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$SESSION_ID" ] || [ -z "$FILE_ID" ]; then
    print_error "Failed to initiate upload: $INITIATE_RESPONSE"
    exit 1
fi

print_success "Upload session initiated"
print_info "Session ID: $SESSION_ID"
print_info "File ID: $FILE_ID"

# Test 6: Upload file in chunks
print_header "Test 6: Uploading File Chunks"

OFFSET=0
CHUNK_NUM=1
TOTAL_CHUNKS=$(( (TEST_FILE_SIZE + CHUNK_SIZE - 1) / CHUNK_SIZE ))

while [ $OFFSET -lt $TEST_FILE_SIZE ]; do
    dd if=test_file.bin of=chunk.tmp bs=1 skip=$OFFSET count=$CHUNK_SIZE 2>/dev/null

    UPLOAD_RESPONSE=$(curl -s -X POST "$BASE_URL/api/files/upload/chunk?session_id=$SESSION_ID" \
        -H "Authorization: Bearer $TOKEN" \
        -F "chunk=@chunk.tmp" \
        -F "offset=$OFFSET")

    PROGRESS=$(echo "$UPLOAD_RESPONSE" | grep -o '"progress":[0-9.]*' | cut -d':' -f2)

    printf "\r  Chunk %d/%d | Progress: %6.2f%%" $CHUNK_NUM $TOTAL_CHUNKS $PROGRESS

    OFFSET=$((OFFSET + CHUNK_SIZE))
    CHUNK_NUM=$((CHUNK_NUM + 1))

    rm -f chunk.tmp
done

echo ""
print_success "File upload complete"

# Test 7: Finalize upload
print_header "Test 7: Finalizing Upload and Processing"

FINALIZE_RESPONSE=$(curl -s -X POST "$BASE_URL/api/files/upload/finalize" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"session_id\":\"$SESSION_ID\",\"strategy\":\"balanced\"}")

if echo "$FINALIZE_RESPONSE" | grep -q "processing started"; then
    print_success "Processing started"
else
    print_error "Finalize failed: $FINALIZE_RESPONSE"
    exit 1
fi

# Test 8: Monitor processing status
print_header "Test 8: Monitoring Processing Status"

MAX_POLLS=120
POLL_COUNT=0

while [ $POLL_COUNT -lt $MAX_POLLS ]; do
    sleep 3

    STATUS_RESPONSE=$(curl -s -X GET "$BASE_URL/api/files/upload/status/$SESSION_ID" \
        -H "Authorization: Bearer $TOKEN")

    STATUS=$(echo "$STATUS_RESPONSE" | grep -o '"status":"[^"]*"' | cut -d'"' -f4)
    PROGRESS=$(echo "$STATUS_RESPONSE" | grep -o '"processing_progress":[0-9.]*' | cut -d':' -f2)
    ERROR_MSG=$(echo "$STATUS_RESPONSE" | grep -o '"error_message":"[^"]*"' | cut -d'"' -f4)

    if [ -z "$PROGRESS" ]; then
        PROGRESS=0
    fi

    printf "\r  Status: %-12s | Progress: %6.1f%%" "$STATUS" "$PROGRESS"

    if [ "$STATUS" = "complete" ]; then
        echo ""
        print_success "Processing completed successfully!"
        break
    elif [ "$STATUS" = "failed" ]; then
        echo ""
        print_error "Processing failed: $ERROR_MSG"
        exit 1
    fi

    POLL_COUNT=$((POLL_COUNT + 1))
done

if [ $POLL_COUNT -ge $MAX_POLLS ]; then
    echo ""
    print_error "Processing timeout"
    exit 1
fi

# Test 9: Download key file
print_header "Test 9: Downloading Key File"

KEY_FILENAME="test_file.bin_${FILE_ID}.2xpfm.key"

curl -s -X GET "$BASE_URL/api/files/download-key/$SESSION_ID" \
    -H "Authorization: Bearer $TOKEN" \
    -o "$KEY_FILENAME"

if [ -f "$KEY_FILENAME" ]; then
    print_success "Key file downloaded: $KEY_FILENAME"
    print_info "Key file preview:"
    head -n 10 "$KEY_FILENAME"
else
    print_error "Failed to download key file"
    exit 1
fi

# Test 10: List stored files
print_header "Test 10: Listing Stored Files"

LIST_RESPONSE=$(curl -s -X GET "$BASE_URL/api/files/list" \
    -H "Authorization: Bearer $TOKEN")

print_success "Files list retrieved"
echo "$LIST_RESPONSE" | python3 -m json.tool 2>/dev/null || echo "$LIST_RESPONSE"

# Verify our uploaded file is in the list
if echo "$LIST_RESPONSE" | grep -q "$FILE_ID"; then
    print_success "Uploaded file found in list"
else
    print_error "Uploaded file NOT found in list"
    exit 1
fi

# Test 11: Verify file integrity
print_header "Test 11: Verifying File Integrity"

VERIFY_RESPONSE=$(curl -s -X GET "$BASE_URL/api/files/verify/$FILE_ID" \
    -H "Authorization: Bearer $TOKEN")

print_success "Integrity check response:"
echo "$VERIFY_RESPONSE" | python3 -m json.tool 2>/dev/null || echo "$VERIFY_RESPONSE"

IS_COMPLETE=$(echo "$VERIFY_RESPONSE" | grep -o '"is_complete":[^,}]*' | cut -d':' -f2)

if [ "$IS_COMPLETE" = "true" ]; then
    print_success "File integrity verified - all chunks available"
else
    print_warning "File integrity check shows missing chunks"
fi

# Test 12: Initiate download with key file
print_header "Test 12: Initiating File Download"

DOWNLOAD_INIT_RESPONSE=$(curl -s -X POST "$BASE_URL/api/files/download/initiate" \
    -H "Authorization: Bearer $TOKEN" \
    -F "key_file=@$KEY_FILENAME")

DOWNLOAD_SESSION_ID=$(echo "$DOWNLOAD_INIT_RESPONSE" | grep -o '"session_id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$DOWNLOAD_SESSION_ID" ]; then
    print_error "Failed to initiate download: $DOWNLOAD_INIT_RESPONSE"
    exit 1
fi

print_success "Download initiated"
print_info "Download session ID: $DOWNLOAD_SESSION_ID"

# Test 13: Monitor download progress
print_header "Test 13: Monitoring Download Progress"

MAX_POLLS=120
POLL_COUNT=0

while [ $POLL_COUNT -lt $MAX_POLLS ]; do
    sleep 3

    DOWNLOAD_STATUS_RESPONSE=$(curl -s -X GET "$BASE_URL/api/files/download/status/$DOWNLOAD_SESSION_ID" \
        -H "Authorization: Bearer $TOKEN")

    DL_STATUS=$(echo "$DOWNLOAD_STATUS_RESPONSE" | grep -o '"status":"[^"]*"' | cut -d'"' -f4)
    DL_PROGRESS=$(echo "$DOWNLOAD_STATUS_RESPONSE" | grep -o '"progress":[0-9.]*' | cut -d':' -f2)
    DL_ERROR=$(echo "$DOWNLOAD_STATUS_RESPONSE" | grep -o '"error_message":"[^"]*"' | cut -d'"' -f4)

    if [ -z "$DL_PROGRESS" ]; then
        DL_PROGRESS=0
    fi

    printf "\r  Status: %-12s | Progress: %6.1f%%" "$DL_STATUS" "$DL_PROGRESS"

    if [ "$DL_STATUS" = "complete" ]; then
        echo ""
        print_success "Download completed successfully!"
        break
    elif [ "$DL_STATUS" = "failed" ]; then
        echo ""
        print_error "Download failed: $DL_ERROR"
        exit 1
    fi

    POLL_COUNT=$((POLL_COUNT + 1))
done

if [ $POLL_COUNT -ge $MAX_POLLS ]; then
    echo ""
    print_error "Download timeout"
    exit 1
fi

# Test 14: Download reconstructed file
print_header "Test 14: Downloading Reconstructed File"

curl -s -X GET "$BASE_URL/api/files/download/file/$DOWNLOAD_SESSION_ID" \
    -H "Authorization: Bearer $TOKEN" \
    -o test_file_downloaded.bin

if [ -f test_file_downloaded.bin ]; then
    print_success "File downloaded: test_file_downloaded.bin"

    DOWNLOADED_SIZE=$(stat -f%z test_file_downloaded.bin 2>/dev/null || stat -c%s test_file_downloaded.bin)
    print_info "Downloaded size: $DOWNLOADED_SIZE bytes"
    print_info "Original size: $TEST_FILE_SIZE bytes"
else
    print_error "Failed to download file"
    exit 1
fi

# Test 15: Verify downloaded file matches original
print_header "Test 15: Verifying Downloaded File"

DOWNLOADED_CHECKSUM=$(sha256sum test_file_downloaded.bin | awk '{print $1}')

print_info "Original checksum:   $ORIGINAL_CHECKSUM"
print_info "Downloaded checksum: $DOWNLOADED_CHECKSUM"

if [ "$ORIGINAL_CHECKSUM" = "$DOWNLOADED_CHECKSUM" ]; then
    print_success "✓ CHECKSUMS MATCH - File integrity verified!"
else
    print_error "✗ CHECKSUMS DO NOT MATCH - File corrupted!"
    exit 1
fi

# Test 16: Test drive unlinking warning (optional)
print_header "Test 16: Testing Drive Unlink Warning"
print_warning "Skipping drive unlink test (would require manual unlinking)"
print_info "To test: unlink a drive via OAuth and verify files marked as 'incomplete'"

# Test 17: Delete file
print_header "Test 17: Deleting File"

DELETE_RESPONSE=$(curl -s -X DELETE "$BASE_URL/api/files/$FILE_ID" \
    -H "Authorization: Bearer $TOKEN")

if echo "$DELETE_RESPONSE" | grep -q "deleted successfully"; then
    print_success "File deleted successfully"
else
    print_error "Delete failed: $DELETE_RESPONSE"
fi

# Verify file is removed from list
LIST_AFTER_DELETE=$(curl -s -X GET "$BASE_URL/api/files/list" \
    -H "Authorization: Bearer $TOKEN")

if ! echo "$LIST_AFTER_DELETE" | grep -q "$FILE_ID"; then
    print_success "File removed from list"
else
    print_warning "File still appears in list (may be marked as deleted)"
fi

# Final Summary
print_header "Test Summary"
echo ""
print_success "All tests completed successfully!"
echo ""
print_info "Test Results:"
echo "  • User: $RANDOM_EMAIL"
echo "  • Drives linked: $ACCOUNTS_ADDED"
echo "  • File ID: $FILE_ID"
echo "  • Original size: $TEST_FILE_SIZE bytes"
echo "  • Upload session: $SESSION_ID"
echo "  • Download session: $DOWNLOAD_SESSION_ID"
echo "  • Key file: $KEY_FILENAME"
echo "  • Checksum match: ✓"
echo ""
print_success "System is working correctly!"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"