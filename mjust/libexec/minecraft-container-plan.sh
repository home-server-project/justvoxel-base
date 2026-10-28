#!/usr/bin/bash
# Image-only planning. Caller has loaded and verified the exact instance record.
jv_container_plan() {
    local live_container active configured_id configured_digest target_id target_digest running='' record_hash state
    image="${JV_MINECRAFT_IMAGE_REPO}:${MINECRAFT_IMAGE_TAG}"
    remote=""
    if [[ ${1:-} != local ]]; then
        remote="$(jv_remote_platform_digest "${image}")" || return 1
        [[ ${remote} =~ ^sha256:[a-f0-9]{64}$ ]] || return 1
    fi
    target="${JV_MINECRAFT_IMAGE_REPO}@${remote}"
    active="$(systemctl show --property=ActiveState --value "${JV_INSTANCE_UNIT}")" || return 1
    case "${active}" in active) ;; inactive|failed) ;; *) return 1 ;; esac
    live_container="$(podman ps --filter "name=^${JV_INSTANCE_CONTAINER}$" --format '{{.Names}}')" || return 1
    if [[ ${active} == active ]]; then [[ ${live_container} == "${JV_INSTANCE_CONTAINER}" ]] || return 1
    else [[ -z ${live_container} ]] || return 1; fi
    configured_id="$(podman image inspect "${JV_INSTANCE_IMAGE_REF}" --format '{{.Id}}' 2>/dev/null || true)"
    configured_digest="$(podman image inspect "${JV_INSTANCE_IMAGE_REF}" --format '{{.Digest}}' 2>/dev/null || true)"
    target_id=""; target_digest=""
    if [[ ${1:-} != local ]]; then
        target_id="$(podman image inspect "${target}" --format '{{.Id}}' 2>/dev/null || true)"
        target_digest="$(podman image inspect "${target}" --format '{{.Digest}}' 2>/dev/null || true)"
    fi
    if [[ ${active} == active ]]; then
        running="$(podman inspect "${JV_INSTANCE_CONTAINER}" --format '{{.Image}}')" || return 1
        [[ -n ${running} ]] || return 1
    fi
    [[ -z ${configured_id} || ${configured_digest} =~ ^sha256:[a-f0-9]{64}$ ]] || return 1
    [[ -z ${target_id} || ${target_digest} == "${remote}" ]] || return 1
    if [[ -z ${configured_id} ]]; then state=image_missing
    elif [[ ${configured_digest} != "${remote}" ]]; then state=update_available
    elif [[ -z ${target_id} ]]; then state=image_missing
    elif [[ ${active} == active && ${running} != "${target_id}" ]]; then state=restart_needed
    else state=up_to_date; fi
    record_hash="$(sha256sum "/etc/justvoxel/instances/${JV_INSTANCE_ID}/config.json")" || return 1
    plan="$(jq -cn --arg id "${JV_INSTANCE_ID}" --arg image "${image}" --arg remote "${remote}" \
        --arg configured "${JV_INSTANCE_IMAGE_REF}" --arg local_id "${configured_id}" --arg local_digest "${configured_digest}" \
        --arg target_id "${target_id}" --arg running "${running}" --arg active "${active}" --arg record "${record_hash}" \
        '{id:$id,image:$image,remote:$remote,configured:$configured,local_id:$local_id,local_digest:$local_digest,target_id:$target_id,running:$running,active:$active,record:$record}')"
    fingerprint="sha256:$(printf '%s' "${plan}" | sha256sum | cut -d' ' -f1)"
    container_state="${state}"
    container_running=false
    [[ ${active} != active ]] || container_running=true
}
