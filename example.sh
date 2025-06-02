#!/bin/bash

source .env

relay=$(echo $BROKER_RELAYS | cut -d',' -f1)
kelay_sk="$KELAY_SECRET"
kelay_pk=$(nak key public "$kelay_sk")
user_sk=$(nak key generate)
user_pk=$(nak key public "$user_sk")
wrap_sk=$(nak key generate)
wrap_pk=$(nak key public "$user_sk")
req='["REQ", "kelay-test", {"kinds": [1], "limit": 1}]'
rumor=$(nak event -k 1507 --sec "$user_sk" --content "$req")
rumor_ct=$(nak encrypt "$rumor" --sec "$user_sk" -p "$kelay_pk")
seal=$(nak event -k 13 --sec "$user_sk" --content "$rumor_ct")
seal_ct=$(nak encrypt "$seal" --sec "$wrap_sk" -p "$kelay_pk")
wrap=$(nak event -k 21059 --sec "$wrap_sk" --content "$seal_ct" -p "$kelay_pk")

# Send the wrapped request
nak event -k 21059 --sec "$wrap_sk" --content "$seal_ct" -p "$kelay_pk" "$relay"

# Listen for responses
nak req --stream -k 21059 -p "$user_pk" "$relay" | while IFS= read -r wrap; do
  seal_pk=$(echo "$wrap" | jq -r '.pubkey')
  seal_ct=$(echo "$wrap" | jq -r '.content')
  seal=$(nak decrypt "$seal_ct" --sec "$user_sk" -p "$seal_pk")
  rumor_pk=$(echo "$seal" | jq -r '.pubkey')
  rumor_ct=$(echo "$seal" | jq -r '.content')
  rumor=$(nak decrypt "$rumor_ct" --sec "$user_sk" -p "$rumor_pk")

  echo "$rumor" | jq -r '.content'
done
