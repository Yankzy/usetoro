#!/usr/bin/env python3
"""
Svix Integration Verification Script

Tests the Svix integration to ensure:
1. Svix client can connect to the server
2. Applications can be created/retrieved
3. Webhooks can be dispatched
4. Message attempts can be retrieved
"""

import os
import sys
import django

# Setup Django environment
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'config.settings')
django.setup()

from webhookks.utils import (
    get_svix_client,
    get_or_create_svix_app,
    dispatch_webhook,
    get_webhook_messages,
    get_webhook_attempts
)
from django.conf import settings
import json


def print_section(title):
    """Print a formatted section header"""
    print(f"\n{'=' * 60}")
    print(f"  {title}")
    print(f"{'=' * 60}\n")


def test_svix_connection():
    """Test 1: Verify Svix client can connect"""
    print_section("TEST 1: Svix Client Connection")
    
    try:
        client = get_svix_client()
        print("✅ Svix client initialized successfully")
        print(f"   Server URL: {settings.SVIX_SERVER_URL}")
        return True
    except Exception as e:
        print(f"❌ Failed to initialize Svix client: {str(e)}")
        return False


def test_app_creation():
    """Test 2: Create/retrieve a test application"""
    print_section("TEST 2: Application Management")
    
    test_user_id = "test-user-123"
    
    try:
        app_id = get_or_create_svix_app(
            user_id=test_user_id,
            app_name="Verification Test App"
        )
        print(f"✅ Application created/retrieved successfully")
        print(f"   App ID: {app_id}")
        return True, app_id
    except Exception as e:
        print(f"❌ Failed to create/retrieve application: {str(e)}")
        return False, None


def test_webhook_dispatch(app_id):
    """Test 3: Dispatch a test webhook"""
    print_section("TEST 3: Webhook Dispatch")
    
    test_user_id = "test-user-123"
    
    try:
        result = dispatch_webhook(
            user_id=test_user_id,
            event_type="test.verification",
            payload={
                "message": "This is a test webhook from the verification script",
                "timestamp": "2026-01-21T01:00:00Z",
                "test_data": {
                    "foo": "bar",
                    "nested": {
                        "value": 42
                    }
                }
            }
        )
        
        if result.get("success"):
            print(f"✅ Webhook dispatched successfully")
            print(f"   Message ID: {result.get('message_id')}")
            print(f"   Event Type: {result.get('event_type')}")
            return True, result.get('message_id')
        else:
            print(f"❌ Webhook dispatch failed: {result.get('error')}")
            return False, None
            
    except Exception as e:
        print(f"❌ Exception during webhook dispatch: {str(e)}")
        return False, None


def test_message_retrieval(user_id="test-user-123"):
    """Test 4: Retrieve webhook messages"""
    print_section("TEST 4: Message Retrieval")
    
    try:
        messages = get_webhook_messages(user_id=user_id, limit=10)
        print(f"✅ Retrieved {len(messages)} webhook messages")
        
        if messages:
            print(f"\n   Latest message:")
            latest = messages[0]
            print(f"     - ID: {latest.get('id')}")
            print(f"     - Event Type: {latest.get('event_type')}")
            print(f"     - Timestamp: {latest.get('timestamp')}")
        
        return True
    except Exception as e:
        print(f"❌ Failed to retrieve messages: {str(e)}")
        return False


def test_attempt_retrieval(user_id="test-user-123"):
    """Test 5: Retrieve webhook attempts"""
    print_section("TEST 5: Attempt Retrieval")
    
    try:
        attempts = get_webhook_attempts(user_id=user_id, limit=10)
        print(f"✅ Retrieved {len(attempts)} webhook attempts")
        
        if attempts:
            print(f"\n   Latest attempt:")
            latest = attempts[0]
            print(f"     - ID: {latest.get('id')}")
            print(f"     - Message ID: {latest.get('msg_id')}")
            print(f"     - Status: {latest.get('status')}")
            print(f"     - Response Code: {latest.get('response_status_code')}")
        
        return True
    except Exception as e:
        print(f"❌ Failed to retrieve attempts: {str(e)}")
        return False


def main():
    """Run all verification tests"""
    print("\n" + "=" * 60)
    print("  SVIX INTEGRATION VERIFICATION")
    print("=" * 60)
    
    results = []
    
    # Test 1: Connection
    results.append(("Connection Test", test_svix_connection()))
    
    # Test 2: App Creation
    success, app_id = test_app_creation()
    results.append(("App Creation Test", success))
    
    # Test 3: Webhook Dispatch
    if success:
        dispatch_success, msg_id = test_webhook_dispatch(app_id)
        results.append(("Webhook Dispatch Test", dispatch_success))
    else:
        results.append(("Webhook Dispatch Test", False))
    
    # Test 4: Message Retrieval
    results.append(("Message Retrieval Test", test_message_retrieval()))
    
    # Test 5: Attempt Retrieval  
    results.append(("Attempt Retrieval Test", test_attempt_retrieval()))
    
    # Summary
    print_section("VERIFICATION SUMMARY")
    
    passed = sum(1 for _, success in results if success)
    total = len(results)
    
    for test_name, success in results:
        status = "✅ PASS" if success else "❌ FAIL"
        print(f"{status}  {test_name}")
    
    print(f"\n{'=' * 60}")
    print(f"Results: {passed}/{total} tests passed")
    print(f"{'=' * 60}\n")
    
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
