import os
import django
import uuid
import sys

# Setup Django environment
sys.path.append('/Users/Yankz/programming/usetoro')
os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'config.settings')
django.setup()

from users.models import User, Activity
from quickbooks.models import QuickBooksConnection
from webhookks.models import WebhookSchema, Connection, DeliveryAttempt, Event
from django.contrib.contenttypes.models import ContentType

def verify_uuid_migration():
    print("Verifying UUID Migration...")

    # Verify User
    user = User.objects.create_user(email=f'test_{uuid.uuid4()}@example.com', password='password')
    print(f"User created with ID: {user.id} (Type: {type(user.id)})")
    assert isinstance(user.id, uuid.UUID), "User ID is not a UUID"

    # Verify Activity
    # Note: Activity needs an object to subscribe to. Using User for simplicity if possible, or ContentType.
    # Activity.object_id is now UUID. We need a model with UUID to link to.
    # User has UUID. Let's subscribe user to themselves (just for testing generic relation).
    
    activity, created = Activity.subscribe(user, user)
    print(f"Activity created: {activity} (object_id: {activity.object_id}, Type: {type(activity.object_id)})")
    assert isinstance(activity.object_id, uuid.UUID), "Activity object_id is not a UUID"
    
    # Verify QuickBooksConnection
    qb_conn = QuickBooksConnection.objects.create(realm_id=str(uuid.uuid4()))
    print(f"QuickBooksConnection created with ID: {qb_conn.id} (Type: {type(qb_conn.id)})")
    assert isinstance(qb_conn.id, uuid.UUID), "QuickBooksConnection ID is not a UUID"

    # Verify WebhookSchema
    schema = WebhookSchema.objects.create(source=f'test_source_{uuid.uuid4()}', schema_definition={})
    print(f"WebhookSchema created with ID: {schema.id} (Type: {type(schema.id)})")
    assert isinstance(schema.id, uuid.UUID), "WebhookSchema ID is not a UUID"

    # Verify Connection
    conn = Connection.objects.create(user=user, source_type='test', secret='secret')
    print(f"Connection created with ID: {conn.id} (Type: {type(conn.id)})")
    assert isinstance(conn.id, uuid.UUID), "Connection ID is not a UUID"

    # Verify DeliveryAttempt
    # Need Event first
    event = Event.objects.create(connection=conn, source='test', payload={})
    delivery = DeliveryAttempt.objects.create(event=event, destination_url='http://example.com', status='pending')
    print(f"DeliveryAttempt created with ID: {delivery.id} (Type: {type(delivery.id)})")
    assert isinstance(delivery.id, uuid.UUID), "DeliveryAttempt ID is not a UUID"

    print("All verifications passed successfully!")

if __name__ == '__main__':
    try:
        verify_uuid_migration()
    except Exception as e:
        print(f"Verification FAILED: {e}")
        sys.exit(1)
