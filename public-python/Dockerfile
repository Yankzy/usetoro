FROM python:3.12-slim

WORKDIR /app

COPY public-python/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY public-python/ .

CMD ["python", "main.py"]
