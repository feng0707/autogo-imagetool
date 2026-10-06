#!/bin/bash
# ============================================================================
# AutoGo 编译环境一键修复脚本
# ----------------------------------------------------------------------------
# 问题：WorkBuddy 自带 PortableGit 的 mingw64\bin 排在真 MSYS2 工具链之前，
#       其旧版 zlib1/libwinpthread/libzstd 被 gcc 后端 cc1.exe 抢加载 ->
#       ABI 不兼容 -> cgo 静默崩溃(exit 127) -> go build 失败。
# 修复：把真 MSYS2 的 /c/msys64/mingw64/bin 强制前置到 PATH 最前（不删任何文件，git 仍可用）。
# 用法：在终端执行   bash fix-autogo-buildenv.sh    然后重开终端
# 幂等：重复执行不会重复添加，已存在则跳过。
# ============================================================================
set -u

MSYS2_BIN="/c/msys64/mingw64/bin"
MARKER="# === AutoGo build fix (MSYS2 mingw64 前置) ==="

# 写一段修复块到目标文件（幂等）
append_fix() {
  local f="$1"
  [ -f "$f" ] || touch "$f"
  if grep -q "$MARKER" "$f"; then
    echo "  已是最新，跳过: $f"
    return
  fi
  cat >> "$f" <<EOF

$MARKER
# 强制把真 MSYS2 的 mingw64/bin 移到 PATH 最前，避免 PortableGit 旧 DLL 被 cc1.exe 抢加载导致 cgo 崩溃。
case ":\$PATH:" in
  *":$MSYS2_BIN:"*)
    PATH="\$(echo "\$PATH" | tr ':' '\n' | grep -v '^$MSYS2_BIN\$' | tr '\n' ':')"
    ;;
esac
export PATH="$MSYS2_BIN:\$PATH"
EOF
  echo "  已写入: $f"
}

echo "==> 1) WorkBuddy PortableGit profile.d（登录 shell 会自动 source）"
PD=""
for cand in "$HOME/.workbuddy/vendor/PortableGit/etc/profile.d"; do
  [ -d "$cand" ] && PD="$cand"
done
if [ -n "$PD" ]; then
  # profile.d 里的独立 .sh 文件，整文件即修复块
  f="$PD/zz-msys2-path.sh"
  if [ -f "$f" ] && grep -q "$MARKER" "$f"; then
    echo "  已是最新，跳过: $f"
  else
    cat > "$f" <<EOF
$MARKER
case ":\$PATH:" in
  *":$MSYS2_BIN:"*)
    PATH="\$(echo "\$PATH" | tr ':' '\n' | grep -v '^$MSYS2_BIN\$' | tr '\n' ':')"
    ;;
esac
export PATH="$MSYS2_BIN:\$PATH"
EOF
    echo "  已写入: $f"
  fi
else
  echo "  未找到 PortableGit profile.d（路径可能已变），请检查 ~/.workbuddy/vendor 下 PortableGit 实际位置"
fi

echo "==> 2) ~/.bashrc"
append_fix "$HOME/.bashrc"
echo "==> 3) ~/.bash_profile"
append_fix "$HOME/.bash_profile"

echo ""
echo "完成。请重开终端（或执行 source ~/.bashrc），之后 go build 即可正常编译。"
echo "验证：新终端里执行  gcc --version  应显示 MSYS2 15.1.0；cc1 不再崩溃。"
