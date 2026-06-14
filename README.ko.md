<div align="center">

# gh-orbit

worktree 마다 흩어진 열린 PR 을 — 커밋 그래프, diff, CI 상태까지 — 터미널 대시보드 한 곳에 모아 주는 `gh` CLI 익스텐션입니다. 키보드에서 손 떼지 않고 각 브랜치의 diff 와 CI 를 살펴본 뒤 PR 을 터미널에서 바로 머지합니다.

[![release](https://img.shields.io/github/v/release/jeonbyeongmin/gh-orbit?color=7c6f9f&label=release)](https://github.com/jeonbyeongmin/gh-orbit/releases)
&nbsp;[![gh extension](https://img.shields.io/badge/gh-extension-24292f?logo=github)](https://github.com/jeonbyeongmin/gh-orbit)
&nbsp;![platform](https://img.shields.io/badge/macOS%20·%20Linux%20·%20Windows-555)

[English](./README.md) · 한국어

<img src="./docs/assets/demo.gif" alt="gh-orbit 데모" width="860">

</div>

---

작업은 여러 worktree 로 흩어지기 마련입니다. 한꺼번에 다루는 여러 기능 브랜치일 수도 있고, 병렬로 돌리는 코딩 에이전트 몇 개일 수도 있습니다. 그리고 worktree 하나하나가 저마다의 커밋 그래프, 아직 커밋하지 않은 diff, CI 가 도는 열린 PR 을 갖고 있습니다. 이걸 다 따라가려면 보통 `git`, `gh`, 그리고 브라우저 탭 수십 개 사이를 계속 오가야 합니다.

gh-orbit 은 이 모두를 한 곳에 모읍니다. worktree 대시보드가 브랜치별 PR 과 CI 상태를 한눈에 보여주고, `→` 로 임의 커밋의 전체 diff 를 열고, `m` 으로 PR 을 머지합니다. git·CI·머지 루프는 터미널 안에서 끝나고, PR 리뷰(코멘트·승인)만 브라우저로 한 번 점프하면 됩니다. git 연산을 재구현하지는 않습니다. 모든 동작이 사용자의 `git` / `gh` 를 그대로 호출(shell-out)하므로 `.gitconfig`, 훅, 커밋 서명, LFS 가 평소 커맨드라인에서와 똑같이 동작합니다.

## 어디에 들어맞나

[lazygit](https://github.com/jesseduffield/lazygit)·[tig](https://github.com/jonas/tig)·[gitui](https://github.com/extrawurst/gitui) 는 워킹 트리 하나의 git(스테이징·리베이스·히스토리 탐색)을 잘 다루고, [jj](https://github.com/jj-vcs/jj) 는 그 밑의 모델 자체를 다시 짭니다. [gh-dash](https://github.com/dlvhdr/gh-dash) 는 GitHub 쪽, 즉 여러 저장소의 PR·이슈를 API 에서 바로 가져오는 일을 잘합니다. 하지만 두 영역은 서로를 보지 못합니다. lazygit 은 PR 이나 CI 를, gh-dash 는 로컬 worktree 나 diff 를 모릅니다.

gh-orbit 은 그 틈을 메웁니다. 브랜치 하나만 있어도 그래프·diff·PR·CI 를 한 곳에서 보고 `m` 으로 머지할 수 있습니다. 진가는 장기 기능 브랜치, 핫픽스, 리뷰용 체크아웃처럼 여러 worktree 를 stash 없이 동시에 띄워 둘 때 드러납니다. 각 worktree 가 자기 PR·CI 에 묶여 있어, 빨간 CI 가 어느 worktree 의 것인지 바로 보이고, 다른 도구로 넘어가 브랜치를 다시 찾을 필요 없이 그 PR 을 머지할 수 있습니다. 동시에 띄워 둔 브랜치가 많을수록 도구를 오가는 수고가 그만큼 줄어듭니다.

## 설치

```bash
gh extension install jeonbyeongmin/gh-orbit
gh orbit
```

git 저장소 안에서 `gh orbit` 을 실행하세요. 그래프·diff·worktree 뷰는 어떤 저장소에서도 동작하지만, PR·CI 기능은 인증된 [`gh`](https://cli.github.com/) CLI(`gh auth login`)와 GitHub 리모트가 있어야 합니다. 없으면 PR·CI 열이 그냥 표시되지 않습니다(에러 없음).

## 실제로 실행되는 git / gh

각 동작은 사용자의 `git` / `gh` 를 실행합니다 — 각 키가 정확히 무엇을 shell-out 하는지 그대로 옮겨둡니다:

| 동작 | 실행 |
| --- | --- |
| 커밋 그래프 (실행 시) | `git log --all --format=…` (그래프 레인은 `git --graph` 가 아니라 gh-orbit 이 그림) |
| `→` patch 오버레이 + `[`/`]` · `{`/`}` | `git show -p <commit>` 을 hunk 단위 · 파일 단위로 |
| Local Changes (`tab` cycle) + `space` | `git status` + `git diff` + `git add` / `git restore --staged` |
| `space` (diff 패널, 선택된 hunk) | hunk 단위 `git apply --cached` (`--reverse` 로 unstage) |
| `space` (체크아웃 / fast-forward) | `git checkout <branch>` / `git merge --ff-only <ref>` |
| `tab` → Worktrees (전환 / 추가 / 제거) | `git worktree list` / `add` / `remove` |
| `b` → `d` (브랜치 삭제) | `git branch -d <branch>` |
| `c` / `R` | `git cherry-pick <commit>` / `git rebase <onto>` |
| `v` / `x` | `git revert <commit>` / `git reset --soft\|--mixed\|--hard <commit>` |
| `n` | `git checkout -b <name> <commit>` |
| `F` / `p` / `P` | `git fetch --all` / `git pull` / `git push` |
| `enter` (PR 웹에서 열기) | `gh pr view <n> --web` |
| `m` (PR 머지) | `gh pr merge --squash\|--merge\|--rebase` |
| Pull Requests 탭 | `gh pr list` (재사용) → `enter` 로 행을 웹에서 열고, `m` 으로 머지 |
| 브랜치 칩의 PR 배지 | `gh pr list` + `gh pr checks <number>` |
| `y` | `git rev-parse <commit>` → 클립보드 |

## 기능

### 커밋 그래프

<img src="./docs/assets/commit-graph.gif" alt="커밋 그래프" width="800">

- 실행 시 모든 로컬 브랜치·리모트·태그(`--all`)를 하나의 통합 그래프로 묶어서 보여준다.
- 점 어휘: `●` 커밋 · `○` 머지 · `◉` HEAD. 레인 색상은 8색 팔레트를 순환한다.
- 각 행은 `그래프 │ 메시지(칩 + 제목) │ 작성자 │ 작성 시각` 으로 읽힌다. 시각은 오른쪽 끝에 고정되어 항상 보이고, 공간이 모자라면 메시지 열이 먼저 잘린다 — 제목이 1 cell 밑으로 줄기 전에 칩·작성자가 그 순서로 빠진다.
- 브랜치/태그 decoration 은 칩으로 그려진다. 매칭되는 열린 PR 에는 `#N` 배지와 CI 롤업 글리프(`✓` 통과 · `✗` 실패 · `○` 진행 중)가 붙고, 실행 시·`r`·매 fetch/pull 후 갱신된다. GitHub 리모트가 없으면 배지를 생략한다.
- reload 는 stale-while-revalidate: 새 그래프가 스트리밍되는 동안 현재 그래프가 화면에 남는다.

### diff 리뷰

<img src="./docs/assets/diff-review.gif" alt="diff 리뷰" width="800">

- `→` 는 focus 커밋의 전체 patch(`git show -p`)를 전체 화면 오버레이로 연다.
- `[` / `]` 는 패치 안에서 hunk 를, `{` / `}` 는 파일을 오가고, 하단에 `<path> [N/M]` 이 표시된다.
- **Local Changes** 페이지(`tab` / `shift+tab` 순환에 포함)는 워킹 트리 diff(파일 트리 + diff 패널)를 Conflicts / Unstaged / Staged 로 나눈다. `→` 로 diff 패널에 들어가고, `←` 로 트리로 돌아가며, `space` 로 focus 파일을 stage/unstage 하고, `r` 로 reload 한다.
- hunk 단위 staging: `tab` 으로 diff 패널에 들어가 `[` / `]` 로 hunk 사이를 이동하고(선택된 `@@` 헤더가 강조됨), `space` 로 그 hunk 만 stage 한다(`git apply --cached`). staged 항목을 보고 있으면 unstage 된다. untracked / conflict 파일은 트리에서 파일 전체 단위로 stage 한다.

### Pull requests (`enter` / `m` / Pull Requests 탭)

<img src="./docs/assets/pull-requests.gif" alt="pull requests" width="800">

리뷰는 GitHub 에서 이뤄집니다. gh-orbit 은 PR 페이지로 바로 띄워 주고, 머지만 터미널에서 처리합니다.

- 칩에 열린 PR 배지가 달린 커밋에서 `enter` 는 그 PR 을 브라우저의 GitHub 에서 연다(`gh pr view --web`). 같은 웹 점프가 Worktree 와 Pull Requests 페이지의 `enter` 에도 연결되어 있다.
- `m` 은 커서 행의 열린 PR 을 중앙 confirm 다이얼로그로 머지한다 — `[s]` squash · `[m]` merge · `[r]` rebase(`gh pr merge`). 머지하면 다이얼로그가 닫히며 PR 배지를 갱신하고, gh 에러(머지 불가, 로그아웃된 `gh`)는 상태 줄에 표시된다.
- **Pull Requests 탭**(`tab` / `shift+tab` 순환의 마지막)은 모든 열린 PR 을 나열한다 — 로컬에 체크아웃되지 않아 그래프 커서가 닿지 못하는 head 브랜치의 PR 까지 포함한다. 행은 `#N <CI 글리프> 제목 · 작성자` 로 읽히고, `enter` 는 커서 행을 웹에서 열고, `m` 은 머지하며, `r` 은 목록을 갱신한다.

### worktree (`tab`)

<img src="./docs/assets/worktrees.gif" alt="worktree 대시보드" width="800">

- `tab` 은 전체화면 대시보드로 순환 이동한다 — worktree 당 3줄 카드(브랜치 + PR/CI + dirty/시각 · 경로 · 마지막 커밋) 로 모든 트리 상태를 한눈에 본다. `tab` / `shift+tab` 으로 다음 / 이전 페이지로 이동한다.
- `space` 는 UI 전체를 다른 worktree 로 프로세스 내부에서 전환한다. `enter` 는 그 worktree 의 열린 PR 을 브라우저의 GitHub 에서 연다 — 각 PR 을 웹에서 리뷰하고, 그래프 / Pull Requests 탭에서 `m` 으로 머지한다.
- `a` 로 worktree 를 추가하고(형제 경로 자동 도출), `d` 로 제거하며(dirty/locked 는 force 확인), `s` 로 마지막 커밋 시각순으로 정렬한다.
- 각 카드에 열린 PR `#N` + CI 배지(있을 때), upstream 대비 `↑a↓b`(ahead/behind), `●N` dirty 마커(`N` = 변경 파일 수), worktree HEAD 의 마지막 커밋 제목·상대 시각이 표시된다.
- `.git/HEAD` 와 `.git/index` 를 fsnotify 로 감시해 외부 커밋·rebase 가 목록을 갱신하고, `r` 은 항상 수동 폴백이다.

### 브랜치 & 체크아웃

<img src="./docs/assets/branches.gif" alt="브랜치 & 체크아웃" width="800">

- 그래프에서 `space` 는 커서의 칩 상태와 HEAD 관계로 체크아웃 / fast-forward / detach 를 고른다. 모호한 행은 브랜치 피커를 연다.
- 체크아웃이 깨끗한 트리를 요구하면 `s` 로 stash 하고 계속하거나, `a` / `esc` 로 중단한다.
- `n` 은 커서에 브랜치를 만들고 전환한다.
- `b` 는 로컬 브랜치를 나열하고 `d` 로 커서 브랜치를 삭제한다(HEAD 보호).

### 히스토리 조작

- `c` 는 커서 커밋을 현재 브랜치에 cherry-pick 하고, `R` 은 현재 브랜치를 커서 커밋 위로 rebase 한다.
- `v` 는 커서 커밋을 revert 한다(새 커밋 기록, 이미 push 된 히스토리에도 안전).
- `x` 는 현재 브랜치를 커서 커밋으로 reset 한다 — `[s]` soft / `[m]` mixed / `[h]` hard. push 된 히스토리를 다시 쓰는 reset 은 거부하고 `v` 로 안내한다.
- 네 가지 모두 확인 우선이며, 충돌은 워킹 트리에 남겨 터미널에서 해결한다.

### 네트워크 & 그 외 키

- `F` fetch(`git fetch --all`) · `p` pull(strategy 해석) · `P` push(첫 push 는 upstream 설정; 절대 force 안 함).
- `y` 는 커밋 해시를 복사하고, `r` 은 refs + log 를 reload 한다.
- `?` 는 현재 페이지 기준의 인라인 help 패널을 토글한다(Global + 그 페이지 키 컬럼).

## 키 바인딩

| 키 | 위치 | 동작 |
| --- | --- | --- |
| `↑` / `↓` · `g` / `G` | 그래프 | 이동 · 맨 위 / 맨 아래로 |
| `space` | 그래프 | 체크아웃 / fast-forward / detach |
| `→` | 그래프 | 전체 화면 patch 오버레이 열기 |
| `enter` | 그래프 | 커서 행의 열린 PR 을 웹에서 열기 |
| `m` | 그래프 | 커서 행의 열린 PR 머지 (`s`/`m`/`r` 전략) |
| `[` / `]` · `{` / `}` | 패치 | 이전 / 다음 hunk · 이전 / 다음 파일 |
| `enter` / `m` | pull requests | 웹에서 열기 / 커서 PR 머지 |
| `tab` / `⇧tab` | 전역 | 페이지 순환 (그래프 · worktree · local changes · pull requests) |
| `space` | local changes | focus 파일 stage / unstage (diff 패널에선 hunk) |
| `[` / `]` | local changes diff | 이전 / 다음 hunk |
| `b` | 전역 | 브랜치 모달 |
| `c` / `R` / `v` / `x` | 그래프 | cherry-pick / rebase / revert / reset |
| `n` | 그래프 | 커서에 브랜치 생성 + 전환 |
| `F` / `p` / `P` | 전역 | fetch / pull / push |
| `y` | 그래프 | 해시 복사 |
| `r` | 전역 | reload |
| `?` | 전역 | help 패널 토글 |
| `ctrl+c` `ctrl+c` | 전역 | 종료(두 번 누르기) |

## 안전장치

gh-orbit 은 절대 force-push 하지 않습니다 — 첫 push 가 upstream 을 설정할 뿐입니다. 히스토리를 다시 쓰는 모든 동작(reset·rebase·revert·브랜치 삭제)은 먼저 확인을 받고, 이미 push 된 히스토리를 다시 쓰는 reset 은 거부하고 `revert` 로 안내합니다. 사용자의 키 입력 없이 실행되는 것은 없습니다.

## 설정

XDG 규격 경로(`internal/config` 가 해석 담당):

- 환경설정 — `$XDG_CONFIG_HOME/gh-orbit/config.toml`(선택):

  ```toml
  [pull]
  strategy = "rebase"   # "ff-only" | "merge" | "rebase"
  ```

  pull strategy 해석 순서: 환경설정 `[pull] strategy` → git config `pull.rebase` → 폴백 `--ff-only`.
- 로그 — `$XDG_STATE_HOME/gh-orbit/log`(TUI 가 stdout 을 소유하므로 런타임 로깅이 여기로 간다).

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

## 라이선스

[MIT](./LICENSE)
