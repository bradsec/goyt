#!/bin/bash
# goyt UI Test Suite
# Tests the web interface using cURL and basic tools

set -e

echo "goyt UI Test Suite"
echo "========================"
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test results
TESTS_PASSED=0
TESTS_FAILED=0

# Helper functions
pass() {
    echo -e "${GREEN}✓${NC} $1"
    ((TESTS_PASSED++))
}

fail() {
    echo -e "${RED}✗${NC} $1"
    ((TESTS_FAILED++))
}

warn() {
    echo -e "${YELLOW}⚠${NC} $1"
}

info() {
    echo -e "ℹ $1"
}

# Test if server is running
test_server_running() {
    info "Testing if server is running..."
    
    if curl -s -o /dev/null --max-time 5 http://localhost:3000/; then
        pass "Server is responding"
    else
        fail "Server is not responding on http://localhost:3000"
        exit 1
    fi
}

# Test main page
test_main_page() {
    info "Testing main page..."
    
    # Test HTTP status
    STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:3000/)
    if [ "$STATUS" = "200" ]; then
        pass "Main page returns 200 OK"
    else
        fail "Main page returns status $STATUS"
    fi
    
    # Test HTML content
    CONTENT=$(curl -s http://localhost:3000/)
    
    if echo "$CONTENT" | grep -q "<!DOCTYPE html>"; then
        pass "Valid HTML5 doctype found"
    else
        fail "HTML5 doctype not found"
    fi
    
    if echo "$CONTENT" | grep -q "goyt"; then
        pass "Page title contains 'goyt'"
    else
        fail "Page title does not contain 'goyt'"
    fi
    
    if echo "$CONTENT" | grep -q '<meta name="viewport"'; then
        pass "Viewport meta tag found (mobile-friendly)"
    else
        fail "Viewport meta tag not found"
    fi
    
    if echo "$CONTENT" | grep -q 'Material+Icons'; then
        pass "Material Icons loaded"
    else
        fail "Material Icons not found"
    fi
    
    # Test semantic HTML
    if echo "$CONTENT" | grep -q '<header class="header">'; then
        pass "Semantic header element found"
    else
        fail "Semantic header element not found"
    fi
    
    if echo "$CONTENT" | grep -q '<main class="main-content">'; then
        pass "Semantic main element found"
    else
        fail "Semantic main element not found"
    fi
    
    # Test form elements
    if echo "$CONTENT" | grep -q 'id="download-form"'; then
        pass "Download form found"
    else
        fail "Download form not found"
    fi
    
    if echo "$CONTENT" | grep -q 'id="url-input"'; then
        pass "URL input field found"
    else
        fail "URL input field not found"
    fi
    
    # Test accessibility
    if echo "$CONTENT" | grep -q 'aria-label='; then
        pass "ARIA labels found (accessibility)"
    else
        warn "No ARIA labels found"
    fi
    
    if echo "$CONTENT" | grep -q 'aria-hidden='; then
        pass "ARIA hidden attributes found (accessibility)"
    else
        warn "No ARIA hidden attributes found"
    fi
}

# Test CSS files
test_css_files() {
    info "Testing CSS files..."
    
    STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:3000/assets/css/main.css)
    if [ "$STATUS" = "200" ]; then
        pass "Main CSS file loads successfully"
    else
        fail "Main CSS file returns status $STATUS"
    fi
    
    # Test CSS content
    CSS_CONTENT=$(curl -s http://localhost:3000/assets/css/main.css)
    
    if echo "$CSS_CONTENT" | grep -q ":root"; then
        pass "CSS custom properties (variables) found"
    else
        fail "CSS custom properties not found"
    fi
    
    if echo "$CSS_CONTENT" | grep -q "@media"; then
        pass "Responsive design media queries found"
    else
        fail "No media queries found"
    fi
    
    if echo "$CSS_CONTENT" | grep -q "focus"; then
        pass "Focus styles found (accessibility)"
    else
        fail "Focus styles not found"
    fi
    
    if echo "$CSS_CONTENT" | grep -q "prefers-reduced-motion"; then
        pass "Reduced motion preferences supported"
    else
        warn "Reduced motion preferences not found"
    fi
    
    if echo "$CSS_CONTENT" | grep -q "prefers-contrast"; then
        pass "High contrast preferences supported"
    else
        warn "High contrast preferences not found"
    fi
}

# Test JavaScript files
test_js_files() {
    info "Testing JavaScript files..."
    
    STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:3000/assets/js/app.js)
    if [ "$STATUS" = "200" ]; then
        pass "Main JavaScript file loads successfully"
    else
        fail "Main JavaScript file returns status $STATUS"
    fi
    
    # Test JavaScript modules
    MODULES=("api-client.js" "download-manager.js" "settings-manager.js" "theme-manager.js" "ui-manager.js")
    
    for module in "${MODULES[@]}"; do
        STATUS=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:3000/assets/js/modules/$module")
        if [ "$STATUS" = "200" ]; then
            pass "JavaScript module $module loads successfully"
        else
            fail "JavaScript module $module returns status $STATUS"
        fi
    done
    
    # Test JavaScript content
    JS_CONTENT=$(curl -s http://localhost:3000/assets/js/app.js)
    
    if echo "$JS_CONTENT" | grep -q "import.*from"; then
        pass "ES6 modules syntax found"
    else
        fail "ES6 modules syntax not found"
    fi
    
    if echo "$JS_CONTENT" | grep -q "class.*{"; then
        pass "ES6 class syntax found"
    else
        fail "ES6 class syntax not found"
    fi
    
    if echo "$JS_CONTENT" | grep -q "addEventListener"; then
        pass "Event listeners found"
    else
        fail "Event listeners not found"
    fi
    
    if echo "$JS_CONTENT" | grep -q "try.*catch"; then
        pass "Error handling found"
    else
        warn "Error handling not found in main app"
    fi
}

# Test PWA features
test_pwa_features() {
    info "Testing PWA features..."
    
    # Test web manifest
    STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:3000/assets/site.webmanifest)
    if [ "$STATUS" = "200" ]; then
        pass "Web manifest loads successfully"
        
        MANIFEST=$(curl -s http://localhost:3000/assets/site.webmanifest)
        if echo "$MANIFEST" | grep -q '"name"'; then
            pass "Web manifest has app name"
        else
            fail "Web manifest missing app name"
        fi
        
        if echo "$MANIFEST" | grep -q '"icons"'; then
            pass "Web manifest has icons"
        else
            fail "Web manifest missing icons"
        fi
    else
        fail "Web manifest returns status $STATUS"
    fi
    
    # Test service worker
    STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:3000/assets/sw.js)
    if [ "$STATUS" = "200" ]; then
        pass "Service worker loads successfully"
        
        SW_CONTENT=$(curl -s http://localhost:3000/assets/sw.js)
        if echo "$SW_CONTENT" | grep -q "addEventListener.*install"; then
            pass "Service worker has install event"
        else
            fail "Service worker missing install event"
        fi
        
        if echo "$SW_CONTENT" | grep -q "addEventListener.*fetch"; then
            pass "Service worker has fetch event"
        else
            fail "Service worker missing fetch event"
        fi
    else
        warn "Service worker returns status $STATUS"
    fi
}

# Test API endpoints
test_api_endpoints() {
    info "Testing API endpoints..."
    
    # Test config endpoint
    STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:3000/api/config)
    if [ "$STATUS" = "200" ]; then
        pass "Config API endpoint responds"
        
        CONFIG=$(curl -s http://localhost:3000/api/config)
        if echo "$CONFIG" | grep -q "{"; then
            pass "Config API returns JSON"
        else
            fail "Config API does not return JSON"
        fi
    else
        fail "Config API endpoint returns status $STATUS"
    fi
    
    # Test downloads endpoint
    STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:3000/api/downloads)
    if [ "$STATUS" = "200" ]; then
        pass "Downloads API endpoint responds"
    else
        fail "Downloads API endpoint returns status $STATUS"
    fi
}

# Test security headers
test_security() {
    info "Testing security headers..."
    
    HEADERS=$(curl -s -I http://localhost:3000/)
    
    if echo "$HEADERS" | grep -q "Access-Control-Allow"; then
        pass "CORS headers found"
    else
        warn "CORS headers not found"
    fi
    
    # Test that sensitive information is not exposed
    CONTENT=$(curl -s http://localhost:3000/)
    if echo "$CONTENT" | grep -qE "(password|secret|key|token)" -i; then
        warn "Potential sensitive information found in HTML"
    else
        pass "No obvious sensitive information in HTML"
    fi
}

# Test performance
test_performance() {
    info "Testing performance..."
    
    # Test response times
    MAIN_TIME=$(curl -s -o /dev/null -w "%{time_total}" http://localhost:3000/)
    CSS_TIME=$(curl -s -o /dev/null -w "%{time_total}" http://localhost:3000/assets/css/main.css)
    JS_TIME=$(curl -s -o /dev/null -w "%{time_total}" http://localhost:3000/assets/js/app.js)
    
    # Convert to milliseconds for easier reading
    MAIN_MS=$(echo "$MAIN_TIME * 1000" | bc -l | cut -d. -f1)
    CSS_MS=$(echo "$CSS_TIME * 1000" | bc -l | cut -d. -f1)
    JS_MS=$(echo "$JS_TIME * 1000" | bc -l | cut -d. -f1)
    
    if [ "$MAIN_MS" -lt 1000 ]; then
        pass "Main page loads quickly (${MAIN_MS}ms)"
    else
        warn "Main page loads slowly (${MAIN_MS}ms)"
    fi
    
    if [ "$CSS_MS" -lt 500 ]; then
        pass "CSS loads quickly (${CSS_MS}ms)"
    else
        warn "CSS loads slowly (${CSS_MS}ms)"
    fi
    
    if [ "$JS_MS" -lt 500 ]; then
        pass "JavaScript loads quickly (${JS_MS}ms)"
    else
        warn "JavaScript loads slowly (${JS_MS}ms)"
    fi
    
    # Test gzip compression (if enabled)
    GZIP_HEADER=$(curl -s -H "Accept-Encoding: gzip" -I http://localhost:3000/assets/css/main.css | grep -i "content-encoding")
    if echo "$GZIP_HEADER" | grep -q "gzip"; then
        pass "Gzip compression enabled"
    else
        warn "Gzip compression not detected"
    fi
}

# Main test execution
main() {
    test_server_running
    echo ""
    
    test_main_page
    echo ""
    
    test_css_files
    echo ""
    
    test_js_files
    echo ""
    
    test_pwa_features
    echo ""
    
    test_api_endpoints
    echo ""
    
    test_security
    echo ""
    
    test_performance
    echo ""
    
    # Summary
    echo "Test Results Summary"
    echo "===================="
    echo -e "${GREEN}Passed: $TESTS_PASSED${NC}"
    echo -e "${RED}Failed: $TESTS_FAILED${NC}"
    echo ""
    
    if [ $TESTS_FAILED -eq 0 ]; then
        echo -e "${GREEN}🎉 All critical tests passed!${NC}"
        echo "The goyt web interface appears to be working correctly."
        exit 0
    else
        echo -e "${RED}❌ Some tests failed.${NC}"
        echo "Please review the failures above and fix any issues."
        exit 1
    fi
}

# Check dependencies
check_dependencies() {
    if ! command -v curl &> /dev/null; then
        echo "Error: curl is required but not installed."
        exit 1
    fi
    
    if ! command -v bc &> /dev/null; then
        echo "Warning: bc is not installed. Performance timing will be limited."
    fi
}

# Run tests
check_dependencies
main