import graphene
import json
import redis
from django.conf import settings

# Initialize Redis client with settings or default
REDIS_URL = getattr(settings, 'REDIS_URL', 'redis://localhost:6379/0')
redis_client = redis.from_url(REDIS_URL)

class CreateInvoice(graphene.Mutation):
    status = graphene.String()
    message = graphene.String()

    class Arguments:
        customer_email = graphene.String(required=True)
        amount = graphene.Float(required=True)
        currency = graphene.String(required=False, default_value="USD")

    def mutate(self, info, customer_email, amount, currency):
        payload = {
            'customer_email': customer_email,
            'amount': amount,
            'currency': currency,
        }
        
        try:
             # Push to Redis queue
             # Queue name: toro:quickbooks:write
            redis_client.lpush('toro:quickbooks:write', json.dumps(payload))
            return CreateInvoice(status="queued", message="Invoice creation task queued")
        except Exception as e:
            raise Exception(str(e))

class Mutation(graphene.ObjectType):
    create_invoice = CreateInvoice.Field()
