#!/usr/bin/env bash
# fix-commits.sh
# 功能：批量修改指定分支上某些 commit 的 Author / Co-authored-by，
#       并将修改后的 commit cherry-pick 到当前分支。
#
# 使用方式：
#   ./fix-commits.sh -b <source-branch> \
#                    -c <commit-sha>[,<commit-sha>,...] \
#                    -n <new-author-name> \
#                    -e <new-author-email> \
#                    [-t <co-author-name> <co-author-email>] \
#                    [-y]
#
# 参数说明：
#   -b  源分支名称（必填）
#   -c  需要修改的 commit SHA 列表，逗号分隔（必填）
#   -n  新的 Author 名称（必填）
#   -e  新的 Author 邮箱（必填）
#   -t  追加 Co-authored-by 信息，格式：-t "姓名" "邮箱"（可选，可多次指定）
#   -y  跳过交互确认，直接执行（可选）

set -euo pipefail

# ─────────────────────────────────────────────
# 颜色输出工具函数
# ─────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
RESET='\033[0m'

info()    { echo -e "${BLUE}[INFO]${RESET}  $*"; }
success() { echo -e "${GREEN}[OK]${RESET}    $*"; }
warn()    { echo -e "${YELLOW}[WARN]${RESET}  $*"; }
error()   { echo -e "${RED}[ERROR]${RESET} $*" >&2; }
step()    { echo -e "${CYAN}${BOLD}==> $*${RESET}"; }

# ─────────────────────────────────────────────
# 显示帮助信息
# ─────────────────────────────────────────────
usage() {
    cat <<EOF
${BOLD}用法：${RESET}
  $(basename "$0") -b <source-branch> -c <sha1>[,<sha2>,...] -n <name> -e <email> \\
                   [-t <co-name> <co-email>] [-y]

${BOLD}参数：${RESET}
  -b <branch>   源分支名称（必填）
  -c <shas>     需要修改的 commit SHA（逗号分隔，支持短 SHA，必填）
  -n <name>     新 Author 名称（必填）
  -e <email>    新 Author 邮箱（必填）
  -t <n> <e>    追加 Co-authored-by（可多次使用）
  -y            跳过交互确认，自动执行
  -h            显示此帮助信息

${BOLD}示例：${RESET}
  # 修改 main 分支上两个 commit 的作者，并 cherry-pick 到当前分支
  $(basename "$0") \\
    -b main \\
    -c abc1234,def5678 \\
    -n "Zhang San" \\
    -e "zhangsan@example.com" \\
    -t "Li Si" "lisi@example.com"
EOF
}

# ─────────────────────────────────────────────
# 全局变量初始化
# ─────────────────────────────────────────────
SOURCE_BRANCH=""
COMMIT_SHAS=()        # 逗号分隔后拆分成数组
NEW_AUTHOR_NAME=""
NEW_AUTHOR_EMAIL=""
CO_AUTHORS=()         # 元素格式："名称 <邮箱>"
AUTO_YES=false

ORIGINAL_BRANCH=""    # 脚本启动时所在分支
TEMP_BRANCH=""        # 临时操作分支名

# ─────────────────────────────────────────────
# 解析命令行参数
# ─────────────────────────────────────────────
parse_args() {
    if [[ $# -eq 0 ]]; then
        usage
        exit 1
    fi

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -b)
                [[ $# -lt 2 ]] && { error "-b 需要提供分支名称"; exit 1; }
                SOURCE_BRANCH="$2"; shift 2 ;;
            -c)
                [[ $# -lt 2 ]] && { error "-c 需要提供 commit SHA"; exit 1; }
                # 将逗号分隔的 SHA 拆分为数组
                IFS=',' read -r -a COMMIT_SHAS <<< "$2"; shift 2 ;;
            -n)
                [[ $# -lt 2 ]] && { error "-n 需要提供 Author 名称"; exit 1; }
                NEW_AUTHOR_NAME="$2"; shift 2 ;;
            -e)
                [[ $# -lt 2 ]] && { error "-e 需要提供 Author 邮箱"; exit 1; }
                NEW_AUTHOR_EMAIL="$2"; shift 2 ;;
            -t)
                # 接收两个参数：名称 + 邮箱
                [[ $# -lt 3 ]] && { error "-t 需要提供 Co-author 名称和邮箱两个参数"; exit 1; }
                CO_AUTHORS+=("$2 <$3>"); shift 3 ;;
            -y)
                AUTO_YES=true; shift ;;
            -h|--help)
                usage; exit 0 ;;
            *)
                error "未知参数：$1"; usage; exit 1 ;;
        esac
    done
}

# ─────────────────────────────────────────────
# 参数校验
# ─────────────────────────────────────────────
validate_args() {
    local has_error=false

    if [[ -z "$SOURCE_BRANCH" ]]; then
        error "必须通过 -b 指定源分支名称"
        has_error=true
    fi

    if [[ ${#COMMIT_SHAS[@]} -eq 0 ]]; then
        error "必须通过 -c 指定至少一个 commit SHA"
        has_error=true
    fi

    if [[ -z "$NEW_AUTHOR_NAME" ]]; then
        error "必须通过 -n 指定新 Author 名称"
        has_error=true
    fi

    if [[ -z "$NEW_AUTHOR_EMAIL" ]]; then
        error "必须通过 -e 指定新 Author 邮箱"
        has_error=true
    fi

    # 简单校验邮箱格式（含 @ 即可）
    if [[ -n "$NEW_AUTHOR_EMAIL" && "$NEW_AUTHOR_EMAIL" != *@* ]]; then
        error "Author 邮箱格式不合法：$NEW_AUTHOR_EMAIL"
        has_error=true
    fi

    $has_error && exit 1
    return 0
}

# ─────────────────────────────────────────────
# 检查运行环境依赖
# ─────────────────────────────────────────────
check_dependencies() {
    step "检查运行依赖"
    local missing=false

    for cmd in git; do
        if ! command -v "$cmd" &>/dev/null; then
            error "缺少必要命令：$cmd"
            missing=true
        fi
    done

    $missing && exit 1

    # 确认当前目录是 git 仓库
    if ! git rev-parse --git-dir &>/dev/null; then
        error "当前目录不是 Git 仓库，请在仓库根目录下执行本脚本"
        exit 1
    fi

    success "依赖检查通过"
}

# ─────────────────────────────────────────────
# 检查工作区是否干净（避免意外覆盖本地修改）
# ─────────────────────────────────────────────
check_clean_workdir() {
    if ! git diff --quiet || ! git diff --cached --quiet; then
        error "工作区存在未提交的改动，请先 stash 或 commit 后再运行本脚本"
        exit 1
    fi
}

# ─────────────────────────────────────────────
# 校验指定分支和 commit 是否存在
# ─────────────────────────────────────────────
validate_git_refs() {
    step "校验 Git 引用合法性"

    # 校验源分支是否存在（本地或远程）
    if ! git rev-parse --verify "refs/heads/$SOURCE_BRANCH" &>/dev/null \
       && ! git rev-parse --verify "refs/remotes/origin/$SOURCE_BRANCH" &>/dev/null; then
        error "源分支不存在：$SOURCE_BRANCH（本地和 origin 均未找到）"
        exit 1
    fi

    # 逐一校验 commit SHA
    for sha in "${COMMIT_SHAS[@]}"; do
        sha_trimmed="${sha// /}"  # 去除可能的空格
        if ! git cat-file -t "$sha_trimmed" &>/dev/null; then
            error "Commit 不存在或无法解析：$sha_trimmed"
            exit 1
        fi
        local obj_type
        obj_type=$(git cat-file -t "$sha_trimmed")
        if [[ "$obj_type" != "commit" ]]; then
            error "引用 '$sha_trimmed' 不是一个 commit 对象（类型：$obj_type）"
            exit 1
        fi
    done

    success "所有引用校验通过"
}

# ─────────────────────────────────────────────
# 显示操作摘要，供用户确认
# ─────────────────────────────────────────────
show_summary() {
    echo ""
    echo -e "${BOLD}操作摘要${RESET}"
    echo -e "  源分支     ：${CYAN}$SOURCE_BRANCH${RESET}"
    echo -e "  当前分支   ：${CYAN}$ORIGINAL_BRANCH${RESET}"
    echo -e "  新 Author  ：${CYAN}$NEW_AUTHOR_NAME <$NEW_AUTHOR_EMAIL>${RESET}"
    if [[ ${#CO_AUTHORS[@]} -gt 0 ]]; then
        echo -e "  Co-authors ："
        for co in "${CO_AUTHORS[@]}"; do
            echo -e "               ${CYAN}$co${RESET}"
        done
    fi
    echo -e "  处理 commit："
    for sha in "${COMMIT_SHAS[@]}"; do
        local msg
        msg=$(git log --oneline -1 "${sha// /}" 2>/dev/null || echo "(无法获取提交信息)")
        echo -e "    ${YELLOW}${sha// /}${RESET}  $msg"
    done
    echo ""
}

# ─────────────────────────────────────────────
# 交互确认（除非传入 -y）
# ─────────────────────────────────────────────
confirm() {
    if $AUTO_YES; then
        info "已指定 -y，跳过确认，自动执行"
        return 0
    fi

    read -r -p "$(echo -e "${YELLOW}确认执行以上操作？[y/N]${RESET} ")" answer
    case "$answer" in
        [yY]|[yY][eE][sS]) return 0 ;;
        *) info "已取消操作"; exit 0 ;;
    esac
}

# ─────────────────────────────────────────────
# 构造修改 commit 时使用的 commit-msg 后缀
# 将 Co-authored-by 追加到提交消息末尾
# ─────────────────────────────────────────────
build_coauthor_trailer() {
    local original_msg="$1"
    local result="$original_msg"

    if [[ ${#CO_AUTHORS[@]} -gt 0 ]]; then
        # Git trailer 规范：消息主体与 trailer 之间须有空行
        result="${result}"$'\n'
        for co in "${CO_AUTHORS[@]}"; do
            result="${result}"$'\n'"Co-authored-by: $co"
        done
    fi

    echo "$result"
}

# ─────────────────────────────────────────────
# 核心逻辑：
#   1. 切到源分支
#   2. 创建临时分支
#   3. 对指定 commit 逐一做 rebase -i 重写 Author 和 message
#   4. cherry-pick 到原始分支
#   5. 清理临时分支
# ─────────────────────────────────────────────
rewrite_and_cherry_pick() {
    step "开始批量重写 commit 并 cherry-pick"

    # 记录处理成功的新 SHA，用于最终报告
    local new_shas=()

    for sha in "${COMMIT_SHAS[@]}"; do
        sha="${sha// /}"  # 去除空格
        local full_sha
        full_sha=$(git rev-parse "$sha")

        info "处理 commit：$full_sha"

        # 获取原始提交消息
        local orig_msg
        orig_msg=$(git log -1 --format="%B" "$full_sha")

        # 构造新提交消息（追加 Co-authored-by）
        local new_msg
        new_msg=$(build_coauthor_trailer "$orig_msg")

        # 使用 git commit-tree 重写单个 commit：
        #   - 保留原始 tree（文件内容不变）
        #   - 保留原始 parent
        #   - 替换 Author 和 Committer
        local parent_args=()
        while IFS= read -r parent; do
            parent_args+=(-p "$parent")
        done < <(git log -1 --format="%P" "$full_sha" | tr ' ' '\n' | grep -v '^$')

        local tree
        tree=$(git log -1 --format="%T" "$full_sha")

        local new_sha
        new_sha=$(
            GIT_AUTHOR_NAME="$NEW_AUTHOR_NAME" \
            GIT_AUTHOR_EMAIL="$NEW_AUTHOR_EMAIL" \
            GIT_AUTHOR_DATE="$(git log -1 --format="%ad" --date=raw "$full_sha")" \
            GIT_COMMITTER_NAME="$NEW_AUTHOR_NAME" \
            GIT_COMMITTER_EMAIL="$NEW_AUTHOR_EMAIL" \
            GIT_COMMITTER_DATE="$(git log -1 --format="%cd" --date=raw "$full_sha")" \
            git commit-tree "$tree" "${parent_args[@]}" -m "$new_msg"
        )

        success "  重写完成：$full_sha → $new_sha"
        new_shas+=("$new_sha")
    done

    # 将所有重写后的 commit cherry-pick 到当前（原始）分支
    step "Cherry-pick 重写后的 commit 到分支 '$ORIGINAL_BRANCH'"

    git checkout "$ORIGINAL_BRANCH" --quiet

    for new_sha in "${new_shas[@]}"; do
        info "  cherry-pick：$new_sha"
        if ! git cherry-pick "$new_sha"; then
            error "cherry-pick 失败（$new_sha），请手动解决冲突后继续"
            error "解决冲突后执行：git cherry-pick --continue"
            exit 1
        fi
        success "  cherry-pick 成功：$new_sha"
    done
}

# ─────────────────────────────────────────────
# 创建临时分支（基于源分支）
# ─────────────────────────────────────────────
create_temp_branch() {
    TEMP_BRANCH="fix-commits-tmp-$$"
    step "在源分支 '$SOURCE_BRANCH' 上创建临时工作分支：$TEMP_BRANCH"

    # 先确保本地源分支是最新的
    if git rev-parse --verify "refs/heads/$SOURCE_BRANCH" &>/dev/null; then
        git checkout "$SOURCE_BRANCH" --quiet
    else
        # 从远程检出
        git checkout -b "$SOURCE_BRANCH" "origin/$SOURCE_BRANCH" --quiet
    fi

    git checkout -b "$TEMP_BRANCH" --quiet
    success "临时分支已创建：$TEMP_BRANCH"
}

# ─────────────────────────────────────────────
# 清理临时分支
# ─────────────────────────────────────────────
cleanup_temp_branch() {
    if [[ -n "$TEMP_BRANCH" ]] && git rev-parse --verify "refs/heads/$TEMP_BRANCH" &>/dev/null; then
        step "清理临时分支：$TEMP_BRANCH"
        # 先切回原始分支，避免删除当前分支失败
        git checkout "$ORIGINAL_BRANCH" 2>/dev/null || true
        git branch -D "$TEMP_BRANCH" 2>/dev/null || true
        success "临时分支已删除：$TEMP_BRANCH"
    fi
}

# ─────────────────────────────────────────────
# 异常退出时的清理钩子
# ─────────────────────────────────────────────
on_exit() {
    local exit_code=$?
    if [[ $exit_code -ne 0 ]]; then
        warn "脚本以非零状态退出（code=$exit_code），尝试清理临时资源..."
        # 尝试切回原始分支
        if [[ -n "$ORIGINAL_BRANCH" ]]; then
            git checkout "$ORIGINAL_BRANCH" --quiet 2>/dev/null || true
        fi
        cleanup_temp_branch
    fi
}

trap on_exit EXIT

# ─────────────────────────────────────────────
# 打印最终操作结果摘要
# ─────────────────────────────────────────────
print_result() {
    echo ""
    echo -e "${GREEN}${BOLD}✔ 全部操作完成！${RESET}"
    echo -e "  当前分支 ${CYAN}$ORIGINAL_BRANCH${RESET} 已包含重写后的 commit。"
    echo -e "  Author  ：${CYAN}$NEW_AUTHOR_NAME <$NEW_AUTHOR_EMAIL>${RESET}"
    if [[ ${#CO_AUTHORS[@]} -gt 0 ]]; then
        echo -e "  Co-authors 已追加至提交消息 trailer。"
    fi
    echo ""
    echo -e "  如需推送，请执行："
    echo -e "    ${YELLOW}git push origin $ORIGINAL_BRANCH${RESET}          # 普通推送"
    echo -e "    ${YELLOW}git push --force-with-lease origin $ORIGINAL_BRANCH${RESET}  # 如历史已分叉"
    echo ""
}

# ─────────────────────────────────────────────
# 主流程入口
# ─────────────────────────────────────────────
main() {
    parse_args "$@"
    validate_args
    check_dependencies
    check_clean_workdir

    # 记录脚本启动时所在分支
    ORIGINAL_BRANCH=$(git rev-parse --abbrev-ref HEAD)
    if [[ "$ORIGINAL_BRANCH" == "HEAD" ]]; then
        error "当前处于 detached HEAD 状态，请先切换到一个具名分支"
        exit 1
    fi

    validate_git_refs
    show_summary
    confirm

    # 在临时分支上做重写，完成后 cherry-pick 回原始分支
    create_temp_branch
    rewrite_and_cherry_pick
    cleanup_temp_branch

    print_result
}

main "$@"
