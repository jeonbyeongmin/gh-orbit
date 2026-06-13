<div align="center">

# gh-orbit

**터미널 리뷰 콕핏 — 로컬 커밋 그래프, diff, 브랜치, worktree 를 키보드 중심의 단일 TUI 에서.**

[![release](https://img.shields.io/github/v/release/jeonbyeongmin/gh-orbit?color=7c6f9f&label=release)](https://github.com/jeonbyeongmin/gh-orbit/releases)
&nbsp;[![gh extension](https://img.shields.io/badge/gh-extension-24292f?logo=github)](https://github.com/jeonbyeongmin/gh-orbit)
&nbsp;![platform](https://img.shields.io/badge/macOS%20·%20Linux%20·%20Windows-555)

[English](./README.md) · 한국어

<img src="./docs/assets/demo.gif" alt="gh-orbit 데모" width="860">

</div>

---

## 왜 gh-orbit 인가?

방금 어떤 브랜치에 30분치 작업이 올라왔다. **뭐가 바뀌었고, 어떤 base 기준이며, 살릴까 버릴까?** 지금은 이 질문이 네 개의 창에 흩어져 있다 — 한 터미널의 `git diff`, PR 을 보는 GitHub 웹 UI, 그래프를 보는 `lazygit` 이나 Fork, 다른 worktree 의 `git log` 를 띄운 네 번째 셸.

`gh-orbit` 은 이 루프를 *작성* 패턴이 아니라 *리뷰* 패턴에 맞춘 하나의 TUI 로 합친다.

- **🛰️ 콕핏 하나로 전체 루프.** 커밋 그래프, 전체 화면 diff, 브랜치, worktree, 워킹 트리까지 — 창 전환도, 맥락 상실도 없다.
- **📖 파일 브라우저가 아니라 diff 우선.** diff 는 작성보다 읽는 빈도가 높다. 전체 화면 patch 오버레이가 주력 표면이고, `[` / `]` 로 20개 파일 패치를 순서가 있는 20개의 챕터처럼 읽는다.
- **🌳 worktree 네이티브.** 각 작업 단위가 새 브랜치/worktree 에 올라온다. 그 사이를 **프로세스 내부에서** 전환한다 — 두 번째 터미널도, 다른 트리에서 돌아가는 작업을 건드리는 일도 없다.
- **🔧 바닥은 진짜 git.** 모든 동작이 사용자의 `git` 바이너리로 shell-out 되므로 `.gitconfig`, 훅, 커밋 서명, LFS 가 셸에서와 똑같이 동작한다. 재구현한 git 도, 예상 밖 동작도 없다.
- **🔔 한눈에 보이는 PR 상태.** 브랜치 칩에 열린 PR 배지가 붙는다 — `#N` 과 1-cell CI 롤업 글리프(`✓` 통과 / `✗` 실패 / `○` 진행 중) — 백그라운드 `gh pr list` 가 채운다.
- **⌨️ 키보드 중심으로 빠르게.** vim 스타일 이동, 모달 오버레이, git 에서 절대 블로킹하지 않음. [Bubble Tea](https://github.com/charmbracelet/bubbletea) 기반.

미감은 Fork 보다 `tig` 에 가깝다 — 위쪽엔 빽빽한 커밋 콕핏, 실제 diff 작업은 모달 patch 뷰어에서.

## 무엇을 대체하나

모든 동작의 바닥은 사용자의 진짜 `git` / `gh` 다 — `gh-orbit` 은 재구현이 아니라 콕핏이다. 원래 네 개의 창에 흩어져 치던 명령들을 키 하나로:

| `gh-orbit` 에서 | 대응하는 `git` / `gh` |
| --- | --- |
| 커밋 그래프 (실행 시 통합) | `git log --all --graph --oneline --decorate` |
| `d` patch 오버레이 + `[` / `]` | `git show -p <commit>` 을 파일 단위로 넘기며 |
| `,` Local Changes + `space` | `git status` + `git diff` + `git add` / `git restore --staged` |
| `enter` (체크아웃 / fast-forward) | `git checkout <branch>` / `git merge --ff-only <ref>` |
| `w` worktree (전환 / 추가 / 제거) | `git worktree list` / `add` / `remove` |
| `b` → `d` (브랜치 삭제) | `git branch -d <branch>` |
| `Z` 좀비 정리 | `git branch --merged` + 수동 `git branch -d` 반복 |
| `c` / `R` | `git cherry-pick <commit>` / `git rebase <onto>` |
| `v` / `x` | `git revert <commit>` / `git reset --soft\|--mixed\|--hard <commit>` |
| `n` | `git checkout -b <name> <commit>` |
| `F` / `p` / `P` | `git fetch --all` / `git pull` / `git push` |
| `o` (PR 열기) | `gh pr view --web <number>` |
| 브랜치 칩의 PR 배지 | `gh pr list` + `gh pr checks <number>` |
| `y` | `git rev-parse <commit>` → 클립보드 |

## 설치

```bash
gh extension install jeonbyeongmin/gh-orbit
gh orbit
```

git 저장소 안에서 `gh orbit` 을 실행한다.

## 기능

### 🛰️ 커밋 그래프 콕핏

- **통합 그래프** — 기본으로 `--all` revision spec 을 walk 하므로 모든 로컬 브랜치·리모트·태그가 하나의 그래프에 담긴다.
- **레인 색상 그래프** — 명확한 점 어휘: `●` 일반 커밋 · `○` 머지 커밋 · `◉` HEAD 행. 레인 색조는 인접 레인이 구분되도록 정렬된 8색 팔레트를 순환한다.
- **읽기 쉬운 커밋 행** — 각 행은 `그래프 │ 메시지(칩 + 제목) │ 작성자 │ 작성 시각` 순으로 읽힌다. 시각은 오른쪽 끝에 고정되어 항상 보이고, 메시지 열이 truncation 을 흡수하며, 제목이 1 cell 밑으로 줄기 전에 칩과 작성자가 (그 순서로) 먼저 빠진다.
- **ref 칩** — 브랜치/태그 decoration 이 제목 앞쪽에 타입이 구분된 칩으로 렌더된다.
- **PR 배지** — 매칭되는 열린 PR 에 대해 브랜치 칩에 `#N` + CI 롤업 글리프(`✓`/`✗`/`○`)가 표시되고, 시작 시·`r`·매 fetch/pull 후 갱신된다. GitHub 리모트가 없는 저장소는 조용히 degrade 한다.
- **깜박임 없는 reload** — stale-while-revalidate: 새 그래프가 스트리밍되는 동안 현재 그래프가 화면에 남는다. 로딩 상태는 단일 gated 스피너로 애니메이션된다.

### 📖 diff 리뷰

- **`d` — 전체 화면 patch 오버레이** — focus 커밋의 `git show -p` 전체 본문.
- **`[` / `]` — 파일 간 점프** — 패치 안에서 파일을 오간다. 하단 힌트가 `<path> [N/M]` 을 보여줘 커서가 어느 파일에 있는지 항상 알 수 있다.
- **`,` — Local Changes 뷰** — 워킹 트리 diff(파일 트리 + diff 패널)를 Conflicts / Unstaged / Staged 섹션으로 나눠, 아직 커밋되지 않은 변경을 TUI 를 떠나지 않고 리뷰한다.
  - `space` focus 파일 stage / unstage · `tab` 트리 ↔ diff focus 순환 · `r` reload · `j`/`k`/`g`/`G` 이동.

### 🌳 worktree (`w`)

- **프로세스 내부 전환** — worktree 를 골라 `enter` 를 누르면 콕핏 전체가 새 트리를 가리킨다. 두 번째 터미널 불필요.
- **추가 / 제거 / 정렬** — `a` 로 worktree 추가(형제 경로 자동 도출), `d` 로 제거(dirty/locked 는 force 확인), `s` 로 마지막 커밋 시각순 정렬.
- **실시간 상태** — 각 행에 `●` dirty 마커, worktree HEAD 의 마지막 커밋 제목 + 상대 시각이 표시된다.
- **외부 변경 감시** — `.git/HEAD` 와 `.git/index` 를 fsnotify 로 감시하므로 다른 셸의 커밋·rebase 가 인벤토리를 자동 갱신한다(수동 `r` 은 항상 폴백으로 동작).

### 🌿 브랜치 & 체크아웃

- **`enter` — 맥락 인식 체크아웃** — 그래프에서 커서의 칩 상태와 HEAD 관계로 로컬 브랜치 체크아웃 / fast-forward / detach 를 판단한다. 모호한 행은 브랜치 피커를 열고, 리모트-칩 행은 `git pull` 을 이어 붙여 "origin/x 에서 enter" 가 "네트워크와 동기화됨"을 뜻하게 한다.
- **dirty 트리 확인** — 체크아웃이 깨끗한 트리를 요구하면 `s`(stash 후 계속) 또는 `a`/`esc`(중단)를 고른다. stash 는 그대로 남고, 해당 브랜치로 돌아올 때 pop 을 상기시킨다.
- **`n` — 커서에 새 브랜치** 생성 + 전환을 한 번에.
- **`b` — 브랜치 모달** — 모든 로컬 브랜치를 나열하고 `d` 로 삭제(HEAD 는 보호).
- **`Z` — 좀비 정리** — 기본 브랜치에 머지됐고, upstream 이 `[gone]` 이며, 어디에도 체크아웃되지 않은 로컬 브랜치를 한 번의 확인으로 일괄 삭제(reflog 복구 힌트 포함).

### ✂️ 히스토리 조작 & scrap 경로

- **`c` — cherry-pick** — 커서 커밋을 현재 브랜치 위에 적용.
- **`R` — rebase** — 현재 브랜치를 커서 커밋 위로 rebase.
- **`v` — revert** — 커서 커밋을 되돌림(히스토리 보존; 이미 push 된 커밋에도 안전).
- **`x` — reset** — 현재 브랜치를 커서 커밋으로 reset(`[s]` soft / `[m]` mixed / `[h]` hard). push 된 히스토리 reset 은 force-push 가 필요하므로 거부하고 `v` 로 안내한다.
- 네 가지 모두 **확인 우선**이며, 충돌은 그대로 남겨 사용자의 터미널에서 해결한다.

### 🌐 네트워크 & 기타

- **`F` fetch**(`git fetch --all`, 백그라운드) · **`p` pull**(strategy 해석) · **`P` push**(첫 push 는 upstream 자동 설정; 절대 force 안 함).
- **`o`** — focus 커밋의 PR 을 GitHub 에서 열기 · **`y`** — 커밋 해시 복사 · **`r`** — refs + log reload.
- **`?`** — 인라인 help 레퍼런스 패널 토글(Global / Graph / Local Changes 컬럼).

## 키 바인딩

| 키 | 위치 | 동작 |
| --- | --- | --- |
| `j` / `k` · `g` / `G` | 그래프 | 이동 · 맨 위 / 맨 아래로 |
| `enter` | 그래프 | 맥락 인식 체크아웃 / fast-forward / detach |
| `d` | 그래프 | 전체 화면 patch 오버레이 열기 |
| `[` / `]` | 패치 | 이전 / 다음 파일로 점프 |
| `,` | 전역 | Local Changes 뷰(워킹 트리) |
| `space` | local changes | focus 파일 stage / unstage |
| `w` / `b` | 전역 | worktree / 브랜치 모달 |
| `c` / `R` / `v` / `x` | 그래프 | cherry-pick / rebase / revert / reset |
| `n` | 그래프 | 커서에 브랜치 생성 + 전환 |
| `F` / `p` / `P` | 전역 | fetch / pull / push |
| `o` / `y` | 그래프 | GitHub 에서 PR 열기 / 해시 복사 |
| `Z` / `r` | 전역 | 좀비 브랜치 정리 / reload |
| `?` | 전역 | help 레퍼런스 패널 토글 |
| `ctrl+c` `ctrl+c` | 전역 | 종료(두 번 누르기) |

## 설정

XDG 규격 경로(`internal/config` 가 해석을 담당):

- **환경설정** — `$XDG_CONFIG_HOME/gh-orbit/config.toml`(선택):

  ```toml
  [pull]
  strategy = "rebase"   # "ff-only" | "merge" | "rebase"
  ```

  pull strategy 해석 순서: 환경설정 `[pull] strategy` → git config `pull.rebase` → `pull.ff` → 폴백 `--ff-only`.
- **로그** — `$XDG_STATE_HOME/gh-orbit/log`(TUI 가 stdout 을 소유하므로 모든 런타임 로깅이 여기로 간다).

## 로드맵

확정이 아니라 현재 의도 순서:

1. **per-hunk staging** — Local Changes 의 stage/unstage 를 hunk 단위 동작으로 확장.
2. **PR 리뷰 패널** — PR 을 같은 레이아웃으로 가져와 patch 오버레이에서 diff 를 읽고, approve / request-changes / merge 를 인라인으로.

## 개발

```bash
go run ./cmd/orbit                    # cwd 저장소 대상으로 실행
go test ./...                         # 테스트
golangci-lint run                     # 린트
tail -f ~/.local/state/gh-orbit/log   # 런타임 로그 추적 (TUI 가 stdout 소유)
```

데모 GIF 는 [VHS](https://github.com/charmbracelet/vhs) 로 재생성한다:

```bash
go build -o /tmp/orbit-demo ./cmd/orbit
vhs docs/assets/demo.tape
```

기능 단위 레퍼런스는 [`docs/`](./docs/) 참고 — [`docs/index.md`](./docs/index.md) 에서 시작.
