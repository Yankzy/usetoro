from django.http import JsonResponse
from django.views.decorators.csrf import csrf_exempt
import json
import logging

logger = logging.getLogger(__name__)

@csrf_exempt
def plaid_webhook(request):
    if request.method != "POST":
        return JsonResponse({"detail": "Method not allowed"}, status=405)
    try:
        payload = json.loads(request.body.decode("utf-8") or "{}")
    except Exception:
        payload = {}
    logger.info("Plaid webhook received: %s", payload)
    # TODO: enqueue processing (Celery/task) or call existing handlers
    return JsonResponse({"payload": payload}, status=200)