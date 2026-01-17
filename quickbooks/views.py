import json
import redis
from django.http import JsonResponse
from django.views.decorators.csrf import csrf_exempt
from django.conf import settings
from django.views.decorators.http import require_POST

# Initialize Redis client
# Assuming REDIS_URL or typical defaults.
# Check settings for REDIS_URL if available, otherwise default to localhost.
REDIS_URL = getattr(settings, 'REDIS_URL', 'redis://localhost:6379/0')
redis_client = redis.from_url(REDIS_URL)

@csrf_exempt
@require_POST
def create_invoice(request):
    try:
        data = json.loads(request.body)
        customer_email = data.get('customer_email')
        amount = data.get('amount')
        currency = data.get('currency', 'USD')

        if not customer_email or amount is None:
            return JsonResponse({'error': 'Missing required fields: customer_email, amount'}, status=400)

        payload = {
            'customer_email': customer_email,
            'amount': amount,
            'currency': currency,
        }

        # Push to Redis queue
        # Queue name: toro:quickbooks:write
        redis_client.lpush('toro:quickbooks:write', json.dumps(payload))

        return JsonResponse({'status': 'queued', 'message': 'Invoice creation task queued'}, status=202)

    except json.JSONDecodeError:
        return JsonResponse({'error': 'Invalid JSON'}, status=400)
    except Exception as e:
        return JsonResponse({'error': str(e)}, status=500)
