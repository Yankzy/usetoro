from rest_framework import serializers
from .models import Connection, Event, DeliveryAttempt

class ConnectionSerializer(serializers.ModelSerializer):
    class Meta:
        model = Connection
        fields = ['id', 'source_type', 'is_active', 'created_at']

class DeliveryAttemptSerializer(serializers.ModelSerializer):
    class Meta:
        model = DeliveryAttempt
        fields = ['destination_url', 'status', 'response_code', 'attempted_at']

class EventSerializer(serializers.ModelSerializer):
    delivery_attempts = DeliveryAttemptSerializer(many=True, read_only=True)
    
    class Meta:
        model = Event
        fields = ['id', 'source', 'payload', 'status', 'created_at', 'delivery_attempts']
