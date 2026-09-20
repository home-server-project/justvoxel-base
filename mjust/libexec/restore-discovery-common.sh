#!/usr/bin/bash

jv_restore_metadata_summary_json() {
    local metadata="$1" safe

    if [[ ! -f ${metadata} ]]; then
        jq -cn '{status:"missing",metadata:null}'
        return 0
    fi
    if ! jq -e '.schemaVersion == 1' "${metadata}" >/dev/null 2>&1; then
        jq -cn '{status:"invalid",metadata:null}'
        return 0
    fi

    if ! safe="$(jq -c '
        def text_or_null: if type == "string" then . else null end;
        def text_or_unknown: if type == "string" then . else "unknown" end;
        {
            created_at: ((.createdAt // null) | text_or_null),
            minecraft: {
                version_mode: ((.minecraft.versionMode // "unknown") | text_or_unknown),
                configured_version: ((.minecraft.configuredVersion // "unknown") | text_or_unknown),
                server_reported_version: ((.minecraft.serverReportedVersion // null) | text_or_null)
            },
            bedrock: {
                enabled: ((.bedrock.enabled // false) == true),
                geyser_reported_version: ((.bedrock.geyserReportedVersion // null) | text_or_null),
                floodgate_configured: ((.bedrock.floodgateConfigured // false) == true)
            },
            justvoxel: {
                variant: ((.justvoxel.variant // null) | text_or_null)
            }
        }
    ' "${metadata}" 2>/dev/null)"; then
        jq -cn '{status:"invalid",metadata:null}'
        return 0
    fi

    jq -cn --argjson metadata "${safe}" '{status:"valid",metadata:$metadata}'
}

jv_restore_discovery_json() {
    local path="$1" records='[]'
    local epoch size archive epoch_int id created_at metadata_record item

    while IFS="$(printf '\t')" read -r epoch size archive; do
        [[ -n ${archive} ]] || continue
        id="$(basename -- "${archive}")"
        [[ ${id} =~ ^minecraft-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}\.tar\.gz$ ]] || continue
        [[ ${size} =~ ^[0-9]+$ ]] || continue
        epoch_int="${epoch%%.*}"
        [[ ${epoch_int} =~ ^[0-9]+$ ]] || continue
        created_at="$(date -u -d "@${epoch_int}" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null)" || continue
        metadata_record="$(jv_restore_metadata_summary_json "${archive}.meta.json")" || return 1

        item="$(jq -cn \
            --arg id "${id}" \
            --arg created_at "${created_at}" \
            --argjson size_bytes "${size}" \
            --argjson metadata_record "${metadata_record}" \
            '{id:$id,created_at:$created_at,size_bytes:$size_bytes,metadata_status:$metadata_record.status,metadata:$metadata_record.metadata}')" || return 1
        records="$(jq -cn --argjson records "${records}" --argjson item "${item}" '$records + [$item]')" || return 1
    done < <(jv_restore_list_archives "${path}")

    jq -cn --argjson backups "${records}" '{backups:$backups}'
}
