#!/usr/bin/env bash
#
# Shows what an idempotency key buys, by making the same mistake twice.
#
# Repeating a request is not a rare event: a double click, a browser retry, a
# proxy that gives up waiting and sends it again. The question is whether the
# second one issues a second invoice. Run it and watch the numbers.
#
#   ./scripts/demo-idempotency.sh
#
set -euo pipefail

IDENTITY=${IDENTITY_URL:-http://localhost:8083}
STOCK=${STOCK_URL:-http://localhost:8081}
BILLING=${BILLING_URL:-http://localhost:8082}

bold() { printf "\033[1m%s\033[0m\n" "$1"; }
dim()  { printf "\033[2m%s\033[0m\n" "$1"; }

number_of() { python3 -c "import sys,json; print(json.load(sys.stdin)['number'])"; }

# A throwaway account, so the demo never depends on one already existing.
email="idem-$(date +%s)@stockly.com.br"
token=$(curl -sS -X POST "$IDENTITY/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"Demo\",\"email\":\"$email\",\"password\":\"SuperSecret1234\"}" \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")

product=$(curl -sS "$STOCK/products?limit=1" -H "Authorization: Bearer $token" \
  | python3 -c "import sys,json; p=json.load(sys.stdin)['items'][0]; print(p['id'], p['code'])")
product_id=${product% *}
product_code=${product#* }
body="{\"items\":[{\"product_id\":\"$product_id\",\"quantity\":1}]}"

dim "Produto usado nas duas chamadas: $product_code"
echo

bold "1. Duas chamadas COM a mesma chave de idempotência"
key=$(uuidgen)
dim "   Idempotency-Key: $key"
first=$(curl -sS -X POST "$BILLING/invoices" -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' -H "Idempotency-Key: $key" -d "$body" | number_of)
second=$(curl -sS -X POST "$BILLING/invoices" -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' -H "Idempotency-Key: $key" -d "$body" | number_of)

printf "   primeira chamada .... nota #%s\n" "$first"
printf "   segunda chamada ..... nota #%s\n" "$second"
if [ "$first" = "$second" ]; then
  printf "   \033[32m=> a mesma nota. A segunda chamada não emitiu nada.\033[0m\n"
else
  printf "   \033[31m=> notas diferentes: a idempotência não funcionou.\033[0m\n"
  exit 1
fi
echo

bold "2. As mesmas duas chamadas SEM a chave"
third=$(curl -sS -X POST "$BILLING/invoices" -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' -d "$body" | number_of)
fourth=$(curl -sS -X POST "$BILLING/invoices" -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' -d "$body" | number_of)

printf "   primeira chamada .... nota #%s\n" "$third"
printf "   segunda chamada ..... nota #%s\n" "$fourth"
printf "   \033[33m=> duas notas. Sem a chave, o serviço não tem como saber que é a mesma intenção.\033[0m\n"
echo
dim "A chave também vale para a impressão, pelo mesmo motivo."
