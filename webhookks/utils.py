"""
Svix Webhook Service Layer

This module provides utilities for interacting with the self-hosted Svix instance.
It handles webhook dispatch, application management, and message attempt retrieval.
"""

import logging
from typing import Dict, Any, Optional, List
from django.conf import settings
from svix.api import Svix, ApplicationIn, MessageIn

logger = logging.getLogger(__name__)

# Singleton Svix client instance
_svix_client: Optional[Svix] = None


def get_svix_client() -> Svix:
    """
    Get or initialize the Svix client.
    
    Returns:
        Svix: Initialized Svix client instance
        
    Raises:
        ValueError: If SVIX_JWT_SECRET is not configured
    """
    global _svix_client
    
    if _svix_client is None:
        if not settings.SVIX_JWT_SECRET:
            raise ValueError(
                "SVIX_JWT_SECRET is not configured. "
                "Please set it in your environment variables."
            )
        
        _svix_client = Svix(
            auth_token=settings.SVIX_JWT_SECRET,
            server_url=settings.SVIX_SERVER_URL
        )
        logger.info(f"Initialized Svix client: {settings.SVIX_SERVER_URL}")
    
    return _svix_client


def get_or_create_svix_app(user_id: str, app_name: Optional[str] = None) -> str:
    """
    Get or create a Svix application for a Django user.
    
    Maps Django users to Svix applications using user_id as the app_id.
    
    Args:
        user_id: Django user UUID (as string)
        app_name: Optional custom name for the application
        
    Returns:
        str: Svix application ID (app_id)
    """
    client = get_svix_client()
    app_id = f"user_{user_id}"
    
    try:
        # Try to retrieve existing application
        app = client.application.get(app_id)
        logger.debug(f"Retrieved existing Svix app: {app_id}")
        return app.id
    except Exception as e:
        # Application doesn't exist, create it
        logger.info(f"Creating new Svix app for user {user_id}")
        
        app = client.application.create(
            ApplicationIn(
                name=app_name or f"User {user_id}",
                uid=app_id
            )
        )
        logger.info(f"Created Svix app: {app.id}")
        return app.id


def dispatch_webhook(
    user_id: str,
    event_type: str,
    payload: Dict[str, Any],
    channels: Optional[List[str]] = None
) -> Dict[str, Any]:
    """
    Dispatch a webhook event through Svix.
    
    This is the main function to trigger webhook delivery from Django.
    
    Args:
        user_id: Django user UUID (as string) - maps to Svix app_id
        event_type: Event type identifier (e.g., "user.created", "order.completed")
        payload: JSON-serializable payload to send to webhook endpoints
        channels: Optional list of channels for filtering (e.g., ["production"])
        
    Returns:
        dict: Response containing message_id and status
        
    Example:
        >>> result = dispatch_webhook(
        ...     user_id="123e4567-e89b-12d3-a456-426614174000",
        ...     event_type="order.completed",
        ...     payload={"order_id": "ord_123", "amount": 99.99}
        ... )
        >>> print(result["message_id"])
    """
    client = get_svix_client()
    
    try:
        # Ensure the app exists
        app_id = get_or_create_svix_app(user_id)
        
        # Create and send the message
        message = client.message.create(
            app_id=app_id,
            message=MessageIn(
                event_type=event_type,
                payload=payload,
                channels=channels or []
            )
        )
        
        logger.info(
            f"Dispatched webhook | "
            f"app_id={app_id} | "
            f"event_type={event_type} | "
            f"message_id={message.id}"
        )
        
        return {
            "success": True,
            "message_id": message.id,
            "app_id": app_id,
            "event_type": event_type
        }
        
    except Exception as e:
        logger.error(
            f"Failed to dispatch webhook | "
            f"user_id={user_id} | "
            f"event_type={event_type} | "
            f"error={str(e)}"
        )
        
        return {
            "success": False,
            "error": str(e),
            "user_id": user_id,
            "event_type": event_type
        }


def get_webhook_attempts(
    user_id: str,
    limit: int = 50,
    event_types: Optional[List[str]] = None
) -> List[Dict[str, Any]]:
    """
    Retrieve webhook delivery attempts for a user.
    
    This is used by the Django API to provide webhook logs to the frontend.
    
    Args:
        user_id: Django user UUID (as string)
        limit: Maximum number of attempts to retrieve (default: 50)
        event_types: Optional filter by event types
        
    Returns:
        list: List of message attempt dictionaries with status, timestamps, etc.
    """
    client = get_svix_client()
    app_id = f"user_{user_id}"
    
    try:
        # List message attempts for this application
        attempts_list = client.message_attempt.list_by_msg(
            app_id=app_id,
            limit=limit
        )
        
        # Transform to a simpler structure for the API
        attempts = []
        for attempt in attempts_list.data:
            attempts.append({
                "id": attempt.id,
                "msg_id": attempt.msg_id,
                "status": attempt.status,
                "response_status_code": attempt.response_status_code,
                "timestamp": attempt.timestamp.isoformat() if attempt.timestamp else None,
                "endpoint_id": attempt.endpoint_id,
                "url": attempt.url
            })
        
        logger.debug(f"Retrieved {len(attempts)} webhook attempts for user {user_id}")
        return attempts
        
    except Exception as e:
        logger.error(f"Failed to retrieve webhook attempts for user {user_id}: {str(e)}")
        return []


def get_webhook_messages(
    user_id: str,
    limit: int = 50
) -> List[Dict[str, Any]]:
    """
    Retrieve webhook messages for a user.
    
    Args:
        user_id: Django user UUID (as string)
        limit: Maximum number of messages to retrieve
        
    Returns:
        list: List of message dictionaries
    """
    client = get_svix_client()
    app_id = f"user_{user_id}"
    
    try:
        messages_list = client.message.list(
            app_id=app_id,
            limit=limit
        )
        
        messages = []
        for msg in messages_list.data:
            messages.append({
                "id": msg.id,
                "event_type": msg.event_type,
                "payload": msg.payload,
                "channels": msg.channels,
                "timestamp": msg.timestamp.isoformat() if msg.timestamp else None
            })
        
        logger.debug(f"Retrieved {len(messages)} webhook messages for user {user_id}")
        return messages
        
    except Exception as e:
        logger.error(f"Failed to retrieve webhook messages for user {user_id}: {str(e)}")
        return []
