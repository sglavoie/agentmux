#!/usr/bin/env bash

[[ -n "${_CC_COMMON_SORTING_LOADED:-}" ]] && return 0
_CC_COMMON_SORTING_LOADED=1

_first_group() {
	# Extract the alphabetically-first group from a \x1f-delimited groups string.
	# Sorting ensures a stable key regardless of TOML array order.
	# Returns "zzz" if empty (sorts ungrouped to end).
	local raw="$1"
	if [[ -z "$raw" ]]; then
		echo "zzz"
		return
	fi
	local -a arr=()
	IFS=$'\x1f' read -ra arr <<<"$raw"
	if [[ ${#arr[@]} -eq 0 || -z "${arr[0]:-}" ]]; then
		echo "zzz"
		return
	fi
	IFS=$'\n' read -rd '' -a arr < <(printf '%s\n' "${arr[@]}" | sort -f) || true
	echo "${arr[0]}"
}

_build_sort_key() {
	local pin_prefix="$1" account="$2" project_name="$3" groups="$4" use_group="$5"
	if [[ "$use_group" == "true" ]]; then
		local fg
		fg="$(_first_group "$groups")"
		echo "${pin_prefix}:${fg}:${project_name}" | tr '[:upper:]' '[:lower:]'
	else
		echo "${pin_prefix}:${account}:${project_name}" | tr '[:upper:]' '[:lower:]'
	fi
}

compute_insert_location() {
	local socket="$1"
	local new_account="$2"
	local new_title="$3"
	local new_groups="${4:-}"

	_load_icons
	local strip_re
	strip_re="$(_icon_strip_regex)"
	local emoji_re
	emoji_re="$(_override_emoji_re)"

	local use_group=false
	[[ "$_DEFAULT_SORT_SESSIONS" == "group" ]] && use_group=true

	local ls_json
	ls_json="$(kitty @ --to "$socket" ls 2>/dev/null)" || return 0

	local tab_data
	tab_data="$(printf '%s' "$ls_json" | jq -r --arg strip_re "$strip_re" --arg emoji_re "$emoji_re" '
        .[0].tabs // [] | .[] |
        (.title | sub($strip_re; "")) as $stripped |
        (
            [.windows[]? | .user_vars.cc_account // empty | select(. != "")] |
            if length > 0 then .[0] else "" end
        ) as $cc_acct |
        (
            [.windows[]? | .user_vars.cc_groups // empty | select(. != "")] |
            if length > 0 then .[0] else "" end
        ) as $cc_groups |
        select($cc_acct != "" or ($stripped | test("^" + $emoji_re + "?[A-Z]:"))) |
        [(.id | tostring), .title, (if $cc_acct == "" then "-" else $cc_acct end), $stripped, $cc_groups] | @tsv
    ')"

	local -a existing_keys=()
	local -a existing_ids=()
	local has_pinned=false

	if [[ -n "$tab_data" ]]; then
		while IFS=$'\t' read -r tid traw taccount tstripped tgroups; do
			[[ -z "$tid" ]] && continue
			[[ "$taccount" == "-" ]] && taccount=""
			local acct="$taccount"
			local _clean_s
			_clean_s="$(echo "$tstripped" | sed -E "s/^${emoji_re}//")"
			if [[ -z "$acct" && "$_clean_s" =~ ^[A-Z]: ]]; then
				acct="$(_resolve_account_from_initial "${_clean_s:0:1}")"
			fi

			local project_name="$_clean_s"
			if [[ "$project_name" =~ ^[A-Z]: ]]; then
				project_name="${project_name:2}"
			fi

			local pin_prefix="1"
			local _pin_check="$traw"
			_pin_check="${_pin_check#"$_ICON_DASHBOARD"}"
			if [[ "$_pin_check" == "${_ICON_STAR}"* ]]; then
				pin_prefix="0"
				has_pinned=true
			fi
			local sort_key
			sort_key="$(_build_sort_key "$pin_prefix" "$acct" "$project_name" "$tgroups" "$use_group")"
			existing_keys+=("$sort_key")
			existing_ids+=("$tid")
		done <<<"$tab_data"
	fi

	local new_project="$new_title"
	if [[ "$new_project" =~ ^[A-Z]: ]]; then
		new_project="${new_project:2}"
	fi
	local new_key
	new_key="$(_build_sort_key "1" "$new_account" "$new_project" "$new_groups" "$use_group")"

	local -a all_entries=()
	local i
	for ((i = 0; i < ${#existing_keys[@]}; i++)); do
		all_entries+=("${existing_keys[$i]}"$'\t'"existing"$'\t'"${existing_ids[$i]}")
	done
	all_entries+=("${new_key}"$'\t'"new"$'\t'"0")

	local -a sorted_entries=()
	while IFS= read -r entry; do
		sorted_entries+=("$entry")
	done < <(printf '%s\n' "${all_entries[@]}" | sort -f -t $'\t' -k1,1)

	local prev_id=""
	for entry in "${sorted_entries[@]}"; do
		local etype eid
		IFS=$'\t' read -r _ etype eid <<<"$entry"
		if [[ "$etype" == "new" ]]; then
			break
		fi
		prev_id="$eid"
	done

	if [[ -z "$prev_id" ]]; then
		echo "first"
	else
		echo "$prev_id"
	fi
}

sort_tabs() {
	local socket="$1"

	_load_icons
	local strip_re
	strip_re="$(_icon_strip_regex)"
	local emoji_re
	emoji_re="$(_override_emoji_re)"

	local use_group=false
	[[ "$_DEFAULT_SORT_SESSIONS" == "group" ]] && use_group=true

	local ls_json
	ls_json="$(kitty @ --to "$socket" ls 2>/dev/null)" || return 0

	local tab_data
	tab_data="$(printf '%s' "$ls_json" | jq -r --arg strip_re "$strip_re" '
        .[0].tabs // [] | to_entries[] |
        .key as $pos | .value as $tab |
        ($tab.title | sub($strip_re; "")) as $stripped |
        (
            [$tab.windows[]? | .user_vars.cc_account // empty | select(. != "")] |
            if length > 0 then .[0] else "" end
        ) as $cc_acct |
        (
            [$tab.windows[]? | .user_vars.cc_groups // empty | select(. != "")] |
            if length > 0 then .[0] else "" end
        ) as $cc_groups |
        [
            ($tab.id | tostring),
            $tab.title,
            $stripped,
            (if $cc_acct == "" then "-" else $cc_acct end),
            (if $tab.is_focused then "1" else "0" end),
            $cc_groups
        ] | @tsv
    ')"

	[[ -z "$tab_data" ]] && return 0

	local -a tab_ids=() tab_titles=() tab_stripped=() tab_accounts=() tab_focused=()
	local -a tab_pinned=() tab_groups=()
	local -a managed_indices=() unmanaged_indices=()
	local focused_id=""
	local idx=0
	local has_pinned=false

	while IFS=$'\t' read -r tid ttitle tstripped taccount tfocused tgroups; do
		[[ -z "$tid" ]] && continue
		[[ "$taccount" == "-" ]] && taccount=""
		tab_ids+=("$tid")
		tab_titles+=("$ttitle")
		tab_stripped+=("$tstripped")
		tab_focused+=("$tfocused")
		tab_groups+=("$tgroups")

		local _pin_title="$ttitle"
		_pin_title="${_pin_title#"$_ICON_DASHBOARD"}"
		if [[ "$_pin_title" == "${_ICON_STAR}"* ]]; then
			tab_pinned+=("1")
			has_pinned=true
		else
			tab_pinned+=("0")
		fi

		if [[ -n "$taccount" ]]; then
			tab_accounts+=("$taccount")
		elif [[ "$tstripped" =~ ^${emoji_re}?[A-Z]: ]]; then
			local _clean_stripped
			_clean_stripped="$(echo "$tstripped" | sed -E "s/^${emoji_re}//")"
			tab_accounts+=("$(_resolve_account_from_initial "${_clean_stripped:0:1}")")
		else
			tab_accounts+=("")
		fi

		if [[ -n "$taccount" ]] || [[ "$tstripped" =~ ^${emoji_re}?[A-Z]: ]]; then
			managed_indices+=("$idx")
		else
			unmanaged_indices+=("$idx")
		fi

		if [[ "$tfocused" == "1" ]]; then
			focused_id="$tid"
		fi
		idx=$((idx + 1))
	done <<<"$tab_data"

	local total=${#tab_ids[@]}
	[[ $total -le 1 ]] && return 0
	[[ ${#managed_indices[@]} -le 1 && ${#unmanaged_indices[@]} -eq 0 ]] && return 0

	local -a sort_input=()
	local mi
	for mi in "${managed_indices[@]}"; do
		local project_name
		project_name="$(echo "${tab_stripped[$mi]}" | sed -E "s/^${emoji_re}//")"
		if [[ "$project_name" =~ ^[A-Z]: ]]; then
			project_name="${project_name:2}"
		fi
		local pin_prefix="1"
		if [[ "${tab_pinned[$mi]}" == "1" ]]; then
			pin_prefix="0"
		fi
		local sort_key
		sort_key="$(_build_sort_key "$pin_prefix" "${tab_accounts[$mi]}" "$project_name" "${tab_groups[$mi]}" "$use_group")"
		sort_input+=("${sort_key}"$'\t'"${mi}")
	done

	local -a desired_order=()
	if [[ "$_DEFAULT_SORT_SESSIONS" == "alpha" ]] || [[ "$_DEFAULT_SORT_SESSIONS" == "group" ]] || [[ "$has_pinned" == "false" ]]; then
		local -a sorted_managed=()
		if [[ ${#sort_input[@]} -gt 0 ]]; then
			while IFS=$'\t' read -r _key si; do
				sorted_managed+=("$si")
			done < <(printf '%s\n' "${sort_input[@]}" | sort -f -t $'\t' -k1,1)
		fi
		for mi in ${sorted_managed[@]+"${sorted_managed[@]}"}; do
			desired_order+=("$mi")
		done
	else
		local -a pinned_input=()
		local -a unpinned_managed=()
		for mi in "${managed_indices[@]}"; do
			if [[ "${tab_pinned[$mi]}" == "1" ]]; then
				local project_name
				project_name="$(echo "${tab_stripped[$mi]}" | sed -E "s/^${emoji_re}//")"
				if [[ "$project_name" =~ ^[A-Z]: ]]; then
					project_name="${project_name:2}"
				fi
				local sort_key
				sort_key="$(echo "${tab_accounts[$mi]}:${project_name}" | tr '[:upper:]' '[:lower:]')"
				pinned_input+=("${sort_key}"$'\t'"${mi}")
			else
				unpinned_managed+=("$mi")
			fi
		done

		local -a sorted_pinned=()
		if [[ ${#pinned_input[@]} -gt 0 ]]; then
			while IFS=$'\t' read -r _key si; do
				sorted_pinned+=("$si")
			done < <(printf '%s\n' "${pinned_input[@]}" | sort -f -t $'\t' -k1,1)
		fi
		for mi in ${sorted_pinned[@]+"${sorted_pinned[@]}"}; do
			desired_order+=("$mi")
		done
		for mi in ${unpinned_managed[@]+"${unpinned_managed[@]}"}; do
			desired_order+=("$mi")
		done
	fi

	for mi in ${unmanaged_indices[@]+"${unmanaged_indices[@]}"}; do
		desired_order+=("$mi")
	done

	local -a current_ids=()
	local di
	for ((di = 0; di < total; di++)); do
		current_ids+=("${tab_ids[$di]}")
	done

	local -a desired_ids=()
	for di in "${desired_order[@]}"; do
		desired_ids+=("${tab_ids[$di]}")
	done

	local already_sorted=true
	for ((di = 0; di < total; di++)); do
		if [[ "${current_ids[$di]}" != "${desired_ids[$di]}" ]]; then
			already_sorted=false
			break
		fi
	done
	[[ "$already_sorted" == "true" ]] && return 0

	local -a reorder_args=()
	if [[ -n "$focused_id" ]]; then
		reorder_args+=("--focus=$focused_id")
	fi
	reorder_args+=("${desired_ids[@]}")
	kitty @ --to "$socket" kitten cc_reorder_tabs.py "${reorder_args[@]}"
}
