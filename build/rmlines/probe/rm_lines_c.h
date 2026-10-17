/* C declarations for the subset of librm_lines' extern "C" API used by the
 * link probe. Upstream library.h includes C++ headers, so cgo cannot use it.
 * The production wrapper lives in internal/storage/exporter/rmlines. */
#pragma once
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

void setDebugMode(bool debug);
bool getDebugMode(void);
const char *buildTree(const char *rmPath);
int destroyTree(const char *treeId);
const char *getSceneInfo(const char *treeId);
const char *makeRenderer(const char *treeId, int pageType, bool landscape);
int destroyRenderer(const char *rendererId);
void getFrame(const char *rendererId, uint32_t *data, size_t dataSize,
              int x, int y, int frameWidth, int frameHeight,
              int width, int height, bool antialias);
