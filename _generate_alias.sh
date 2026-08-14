#!/usr/bin/env bash

adjectives=(
  "brave" "quiet" "swift" "gentle" "bright" "calm" "bold" "lucky" "sunny" "misty"
  "happy" "clever" "shy" "proud" "wild" "soft" "sharp" "warm" "cool" "fresh"
)

fruits=(
  "apple" "banana" "mango" "papaya" "lychee" "durian" "pineapple" "grape" "melon" "guava"
  "kiwi" "peach" "plum" "cherry" "lemon" "lime" "coconut" "fig" "date" "berry"
)

generate_alias() {
  local adj="${adjectives[RANDOM % ${#adjectives[@]}]}"
  local fruit="${fruits[RANDOM % ${#fruits[@]}]}"
  echo "${adj}-${fruit}"
}

random_index() {
  local max=$1
  echo $(( $(od -An -N2 -tu2 /dev/urandom) % max ))
}

generate_alias_urandom() {
  local adj="${adjectives[$(random_index ${#adjectives[@]})]}"
  local fruit="${fruits[$(random_index ${#fruits[@]})]}"
  echo "${adj}-${fruit}"
}

generate_unique_alias() {
  local base_dir="$1"
  local alias
  local max_attempts=100
  local attempt=0

  while true; do
    alias=$(generate_alias)
    if [[ ! -d "${base_dir}/${alias}" ]]; then
      echo "$alias"
      return 0
    fi

    attempt=$((attempt + 1))
    if (( attempt >= max_attempts )); then
      echo "${alias}-${RANDOM}"
      return 0
    fi
  done
}
